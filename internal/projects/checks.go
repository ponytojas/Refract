package projects

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// Warning is a non-blocking portability problem.
type Warning struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

var (
	reDocumentclass = regexp.MustCompile(`\\documentclass\b`)
	// \setmainfont[opts]{Name}[opts], \newfontfamily\cmd[opts]{Name}[opts] (also {\cmd}).
	reFontCmd = regexp.MustCompile(`\\(setmainfont|setsansfont|setmonofont|newfontfamily)\b\s*(?:\\[A-Za-z@]+|\{\\[A-Za-z@]+\})?\s*(\[[^\]]*\])?\s*\{([^}]*)\}(\s*\[[^\]]*\])?`)
	rePackage = regexp.MustCompile(`\\(?:usepackage|RequirePackage)\s*(\[[^\]]*\])?\s*\{([^}]*)\}`)
	reBibtex  = regexp.MustCompile(`backend\s*=\s*bibtex\b`)
	reFontExt = regexp.MustCompile(`(?i)\.(otf|ttf|ttc|pfb)$`)
)

var shellEscapePackages = []string{"minted", "pythontex", "svg"}

const fontFix = "Put the font files in a fonts/ folder in the project and load them with Path = fonts/, Extension = .otf (or .ttf). Fonts that ship with TeX (Latin Modern etc.) can be loaded by file name."

// Check scans the project's TeX sources for portability problems.
func Check(dir string) ([]Warning, error) {
	var out []Warning
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
		return err == nil
	}
	err := walkFiles(dir, func(rel string, _ fs.FileInfo) error {
		switch strings.ToLower(path.Ext(rel)) {
		case ".tex", ".sty", ".cls", ".ltx":
		default:
			return nil
		}
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		out = append(out, checkSource(rel, string(b), exists)...)
		return nil
	})
	return out, err
}

// exists reports whether a project-relative path exists (used for font Path= checks).
func checkSource(file, src string, exists func(rel string) bool) []Warning {
	src = stripComments(src)
	var out []Warning
	add := func(offset int, kind, msg string) {
		out = append(out, Warning{File: file, Line: 1 + strings.Count(src[:offset], "\n"), Kind: kind, Message: msg})
	}
	// Named families and IfFontExists are resolved at compile time. They are
	// not import problems; an actually missing font is reported by fontspec.
	for _, m := range reFontCmd.FindAllStringSubmatchIndex(src, -1) {
		opts := sub(src, m, 2) + sub(src, m, 4)
		name := strings.TrimSpace(sub(src, m, 3))
		if p := optValue(opts, "Path"); p != "" {
			if msg := checkFontPath(file, name, p, opts, exists); msg != "" {
				add(m[0], "missing-font", msg)
			}
			continue
		}
		if strings.Contains(opts, "Extension") || reFontExt.MatchString(name) {
			continue
		}
	}
	for _, m := range rePackage.FindAllStringSubmatchIndex(src, -1) {
		for pkg := range strings.SplitSeq(sub(src, m, 2), ",") {
			pkg = strings.TrimSpace(pkg)
			switch {
			case pkg == "biblatex" && !reBibtex.MatchString(sub(src, m, 1)):
				add(m[0], "biber", `biblatex without backend=bibtex needs biber, which Tectonic does not include. Load it with \usepackage[backend=bibtex]{biblatex}.`)
			case slices.Contains(shellEscapePackages, pkg):
				add(m[0], "shell-escape", pkg+" needs shell escape, which is disabled because compilation runs untrusted. It will not work.")
			}
		}
	}
	return out
}

// optValue returns key's value in a fontspec option list ("Path = fonts/, ...").
func optValue(opts, key string) string {
	m := regexp.MustCompile(`\b` + key + `\s*=\s*([^,\]\n]+)`).FindStringSubmatch(opts)
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m[1])
}

// checkFontPath verifies that a fontspec Path= folder, and its upright font
// file, are in the project. Path is relative to where TeX runs (the main file's
// folder), which is usually the folder of this file or the project root.
func checkFontPath(file, name, p, opts string, exists func(string) bool) string {
	if strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~") {
		return "Path = " + p + " loads fonts from outside the project, so it only works on this machine. " + fontFix
	}
	var dir string
	for _, d := range []string{path.Join(path.Dir(file), p), path.Clean(p)} {
		if exists(d) {
			dir = d
			break
		}
	}
	if dir == "" {
		return "Path = " + p + " but that folder is not in the project. Add the font files for " + name + " to it."
	}
	ext := optValue(opts, "Extension")
	if ext == "" {
		return ""
	}
	upright := optValue(opts, "UprightFont")
	if upright == "" {
		upright = "*"
	}
	font := path.Join(dir, strings.ReplaceAll(upright, "*", name)+ext)
	if !exists(font) {
		return font + " is not in the project. Add the font files for " + name + " to " + dir + "/."
	}
	return ""
}

func sub(s string, m []int, i int) string {
	if m[2*i] < 0 {
		return ""
	}
	return s[m[2*i]:m[2*i+1]]
}

// stripComments blanks TeX comments (an unescaped % to end of line), keeping
// line structure so offsets still map to line numbers.
func stripComments(src string) string {
	lines := strings.Split(src, "\n")
	for i, l := range lines {
		for j := 0; j < len(l); j++ {
			if l[j] == '\\' {
				j++ // skip the escaped character, e.g. \%
				continue
			}
			if l[j] == '%' {
				lines[i] = l[:j]
				break
			}
		}
	}
	return strings.Join(lines, "\n")
}

// detectMain picks the main file: main.tex if it has \documentclass, else the
// shallowest such .tex file (alphabetical on ties), else "" (ask the user).
// candidates lists every .tex file for that question.
func detectMain(dir string) (main string, candidates []string, err error) {
	var withClass []string
	err = walkFiles(dir, func(rel string, _ fs.FileInfo) error {
		if strings.ToLower(path.Ext(rel)) != ".tex" {
			return nil
		}
		candidates = append(candidates, rel)
		b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
		if err != nil {
			return err
		}
		if reDocumentclass.MatchString(stripComments(string(b))) {
			withClass = append(withClass, rel)
		}
		return nil
	})
	if err != nil {
		return "", nil, err
	}
	if slices.Contains(withClass, "main.tex") {
		return "main.tex", candidates, nil
	}
	slices.SortFunc(withClass, func(a, b string) int {
		if d := strings.Count(a, "/") - strings.Count(b, "/"); d != 0 {
			return d
		}
		return strings.Compare(a, b)
	})
	if len(withClass) > 0 {
		return withClass[0], candidates, nil
	}
	return "", candidates, nil
}
