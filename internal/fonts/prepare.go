package fonts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Prepare returns TeX font setup, or an empty string when no families can be
// resolved. Generated assets stay under .refract; original sources are unchanged.
func (r *Resolver) Prepare(ctx context.Context, projectDir, mainFile string) (string, error) {
	names, err := projectFamilies(projectDir)
	if err != nil {
		return "", err
	}
	if len(names) == 0 {
		return "", nil
	}
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	local, err := localFamilies(projectDir)
	if err != nil {
		return "", err
	}
	downloadCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var entries strings.Builder
	mainDir := filepath.Join(projectDir, filepath.Dir(mainFile))
	for _, name := range names {
		err = nil
		if err := ctx.Err(); err != nil {
			return "", err
		}
		f, isLocal := local[strings.ToLower(name)]
		isLocal = isLocal && f.Regular != ""
		builtin, ok := bundled(name)
		prefix := "Path={},"
		if !isLocal && ok {
			f = builtin
		}
		if isLocal || !ok {
			if !isLocal {
				f, err = r.download(downloadCtx, name)
			}
			if err != nil {
				// A missing family can be an unused fallback in IfFontExistsTF.
				// Let TeX evaluate it, rather than failing before TeX runs.
				continue
			}
			dest := filepath.Join(".refract", "fonts", key(name))
			if err := root.MkdirAll(dest, 0o700); err != nil {
				return "", err
			}
			staged := family{}
			for i, file := range f.files() {
				if file == "" {
					continue
				}
				var b []byte
				if isLocal {
					b, err = root.ReadFile(file)
				} else {
					b, err = os.ReadFile(filepath.Join(r.CacheDir, key(name), file))
				}
				if err != nil {
					return "", err
				}
				filename := []string{"regular", "bold", "italic", "bold-italic"}[i] + filepath.Ext(file)
				if err := root.WriteFile(filepath.Join(dest, filename), b, 0o600); err != nil {
					return "", err
				}
				switch i {
				case 0:
					staged.Regular = filename
				case 1:
					staged.Bold = filename
				case 2:
					staged.Italic = filename
				case 3:
					staged.BoldItalic = filename
				}
			}
			f = staged
			rel, err := filepath.Rel(mainDir, filepath.Join(projectDir, dest))
			if err != nil {
				return "", err
			}
			prefix = "Path={" + filepath.ToSlash(rel) + "/},"
		}
		fmt.Fprintf(&entries, "\\prop_gput:Nnn \\g_refract_fonts_prop {%s} {%sUprightFont={%s},BoldFont={%s},ItalicFont={%s},BoldItalicFont={%s}}\n",
			strings.ReplaceAll(name, " ", "~"), prefix, f.Regular, f.Bold, f.Italic, f.BoldItalic)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if entries.Len() == 0 {
		return "", nil
	}
	return bootstrap(entries.String()), nil
}

func bootstrap(entries string) string {
	var out strings.Builder
	out.WriteString(`\ExplSyntaxOn
\AddToHook{package/fontspec/after}{
\prop_new:N \g_refract_fonts_prop
`)
	out.WriteString(entries)
	out.WriteString(`\cs_new_protected:Npn \refract_options:nn #1#2 {
  \tl_clear:N \l_refract_options_tl
  \regex_match:nnF { (\A|,)\s*(Path|Extension|UprightFont|BoldFont|ItalicFont|BoldItalicFont|FontIndex)\s*= } {#2} {
    \prop_get:NnNF \g_refract_fonts_prop {#1} \l_refract_options_tl { \tl_clear:N \l_refract_options_tl }
  }
}
\tl_new:N \l_refract_options_tl
`)
	for _, command := range []string{"setmainfont", "setsansfont", "setmonofont", "fontspec", "newfontfamily", "newfontface"} {
		// An alias made with let/cs_new_eq would share xparse's implementation
		// slot and recurse after RenewDocumentCommand replaces the public command.
		fmt.Fprintf(&out, "\\NewCommandCopy \\refract_original_%s \\%s\n", command, command)
		if strings.HasPrefix(command, "newfont") {
			fmt.Fprintf(&out, `\RenewDocumentCommand \%s {m O{} m O{}} {
  \refract_options:nn {#3}{#2,#4}
  \use:x { \exp_not:N \refract_original_%s \exp_not:n {#1}
    [\exp_not:V \l_refract_options_tl,\exp_not:n {#2,#4}] {\exp_not:n {#3}} }
}
`, command, command)
		} else {
			fmt.Fprintf(&out, `\RenewDocumentCommand \%s {O{} m O{}} {
  \refract_options:nn {#2}{#1,#3}
  \use:x { \exp_not:N \refract_original_%s
    [\exp_not:V \l_refract_options_tl,\exp_not:n {#1,#3}] {\exp_not:n {#2}} }
}
`, command, command)
		}
	}
	for _, suffix := range []string{"TF", "T", "F"} {
		fmt.Fprintf(&out, "\\cs_if_exist:NT \\IfFontExists%s {\n", suffix)
		fmt.Fprintf(&out, "\\cs_new_eq:NN \\refract_original_exists%s \\IfFontExists%s\n", suffix, suffix)
		switch suffix {
		case "TF":
			out.WriteString(`\RenewDocumentCommand \IfFontExistsTF {m m m} {
  \prop_if_in:NnTF \g_refract_fonts_prop {#1} {#2}{\refract_original_existsTF{#1}{#2}{#3}}
}
`)
		case "T":
			out.WriteString(`\RenewDocumentCommand \IfFontExistsT {m m} {
  \prop_if_in:NnTF \g_refract_fonts_prop {#1} {#2}{\refract_original_existsT{#1}{#2}}
}
`)
		case "F":
			out.WriteString(`\RenewDocumentCommand \IfFontExistsF {m m} {
  \prop_if_in:NnF \g_refract_fonts_prop {#1} {\refract_original_existsF{#1}{#2}}
}
`)
		}
		out.WriteString("}\n")
	}
	out.WriteString("}\n\\ExplSyntaxOff\n")
	return out.String()
}
