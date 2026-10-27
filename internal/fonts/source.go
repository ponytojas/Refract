// Package fonts resolves fontspec families without changing project sources.
package fonts

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

var (
	fontCommand = regexp.MustCompile(`\\(?:setmainfont|setsansfont|setmonofont|fontspec|newfontfamily|newfontface)\b\s*(?:\\[A-Za-z@]+|\{\\[A-Za-z@]+\})?\s*(\[[^\]]*\])?\s*\{([^{}]*)\}(\s*\[[^\]]*\])?`)
	fontExists  = regexp.MustCompile(`\\IfFontExists(?:TF|T|F)\s*\{([^{}]*)\}`)
	fileOptions = regexp.MustCompile(`(?:^|,)\s*(?:Path|Extension|UprightFont|BoldFont|ItalicFont|BoldItalicFont|FontIndex)\s*=`)
	familyName  = regexp.MustCompile(`^[\pL\pN][\pL\pN ._-]{0,99}$`)
)

func validFamily(name string) bool {
	return familyName.MatchString(name) && !strings.ContainsAny(name, `/\`) &&
		!strings.HasSuffix(strings.ToLower(name), ".ttf") && !strings.HasSuffix(strings.ToLower(name), ".otf") &&
		!strings.HasSuffix(strings.ToLower(name), ".ttc")
}

// families only collects literal family names. Macros, commented commands and
// explicit file selections belong to TeX, rather than a partial TeX parser.
func families(src string) []string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		for j := 0; j < len(line); j++ {
			if line[j] == '\\' {
				j++
			} else if line[j] == '%' {
				lines[i] = line[:j]
				break
			}
		}
	}
	src = strings.Join(lines, "\n")
	var out []string
	for _, m := range fontCommand.FindAllStringSubmatch(src, -1) {
		if !fileOptions.MatchString(strings.Trim(m[1], "[]") + "," + strings.TrimSpace(strings.Trim(m[3], " \r\n\t[]"))) {
			if name := strings.TrimSpace(m[2]); validFamily(name) {
				out = append(out, name)
			}
		}
	}
	for _, m := range fontExists.FindAllStringSubmatch(src, -1) {
		if name := strings.TrimSpace(m[1]); validFamily(name) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func projectFamilies(dir string) ([]string, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var names []string
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != dir && (d.Name() == ".git" || d.Name() == ".refract") {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || !slices.Contains([]string{".tex", ".sty", ".cls", ".ltx"}, strings.ToLower(filepath.Ext(p))) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > 2<<20 {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		b, err := root.ReadFile(rel)
		if err == nil {
			names = append(names, families(string(b))...)
		}
		return err
	})
	slices.Sort(names)
	names = slices.Compact(names)
	// Bound work for imported documents containing many invented family names.
	return names[:min(len(names), 32)], err
}
