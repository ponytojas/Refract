// Package refs manages a project's bibliography: citation keys, BibTeX entries
// (always built from a source's metadata, never by a model), which .bib file
// to use, and checking that an edit only added citations.
package refs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ponytojas/Refract/internal/projects"
)

// Ref is a citable work. BibTeX renders its entry under a given key.
type Ref struct {
	Title   string
	Authors []string // "First Last"
	Year    int
	DOI     string // bare and lowercase, e.g. 10.1038/nature14539
	Eprint  string // arXiv id, without version
	BibTeX  func(key string) string
}

var stopwords = map[string]bool{}

// Stopword reports whether w (lowercase) is a common word ignored by keys and searches.
func Stopword(w string) bool { return stopwords[w] }

func init() {
	for _, w := range strings.Fields("a an the on of for in to and or with is are be was were via from by at as it its this that you your we our all not can do") {
		stopwords[w] = true
	}
}

// Folds common Latin accents; other non-ASCII letters are dropped from keys.
var fold = strings.NewReplacer("á", "a", "à", "a", "ä", "a", "â", "a", "ã", "a", "å", "a", "é", "e", "è", "e", "ë", "e", "ê", "e",
	"í", "i", "ì", "i", "ï", "i", "î", "i", "ó", "o", "ò", "o", "ö", "o", "ô", "o", "õ", "o", "ø", "o", "ú", "u", "ù", "u", "ü", "u", "û", "u",
	"ñ", "n", "ç", "c", "ß", "ss", "ł", "l", "š", "s", "ž", "z", "č", "c", "ć", "c", "ř", "r")

func ascii(s string) string {
	s = fold.Replace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Key is a citation key in the usual style: first author's surname, year, first significant title word.
func Key(p Ref) string {
	surname := "anon"
	if len(p.Authors) > 0 {
		f := strings.Fields(p.Authors[0])
		if s := ascii(f[len(f)-1]); s != "" {
			surname = s
		}
	}
	word := ""
	for _, w := range strings.Fields(p.Title) {
		if w = ascii(w); w != "" && !stopwords[w] {
			word = w
			break
		}
	}
	return fmt.Sprintf("%s%d%s", surname, p.Year, word)
}

// Escape protects BibTeX/LaTeX specials that titles carry unescaped.
var special = regexp.MustCompile(`(^|[^\\])([&%#])`)

func Escape(s string) string {
	for special.MatchString(s) { // twice for adjacent specials ("&&")
		s = special.ReplaceAllString(s, `$1\$2`)
	}
	return s
}

var entryKey = regexp.MustCompile(`@\w+\s*\{\s*([^,\s]+)\s*,`)

// AddEntry appends p to a .bib file's content. A work already present (same
// DOI or eprint) is not added again: its existing key is returned. A key taken
// by another entry gets a letter suffix.
func AddEntry(bib string, p Ref) (content, key string, added bool) {
	var same *regexp.Regexp
	switch {
	case p.DOI != "":
		same = regexp.MustCompile(`(?i)doi\s*=\s*[{"]\s*(?:https?://(?:dx\.)?doi\.org/)?` + regexp.QuoteMeta(p.DOI) + `\s*[}"]`)
	case p.Eprint != "":
		same = regexp.MustCompile(`(?i)eprint\s*=\s*[{"]` + regexp.QuoteMeta(p.Eprint) + `(v\d+)?[}"]`)
	}
	if same != nil {
		if m := same.FindStringIndex(bib); m != nil {
			if keys := entryKey.FindAllStringSubmatch(bib[:m[0]], -1); len(keys) > 0 {
				return bib, keys[len(keys)-1][1], false
			}
		}
	}
	taken := map[string]bool{}
	for _, m := range entryKey.FindAllStringSubmatch(bib, -1) {
		taken[m[1]] = true
	}
	base := Key(p)
	key = base
	for c := 'a'; taken[key]; c++ {
		key = base + string(c)
	}
	if bib != "" && !strings.HasSuffix(bib, "\n") {
		bib += "\n"
	}
	if bib != "" {
		bib += "\n"
	}
	return bib + p.BibTeX(key), key, true
}

var bibCommand = regexp.MustCompile(`\\(addbibresource|bibliography)\s*(?:\[[^\]]*\])?\s*\{([^}]*)\}`)

// BibTarget picks the .bib file citations go to: the first one a .tex file
// names (\addbibresource or \bibliography), else a .bib in the folder of a
// main file in a subfolder, else the first .bib in the project, else
// references.bib next to the main file (and needsSetup reports that no .tex
// file loads a bibliography yet). The result is relative to the project and
// never leaves it.
func BibTarget(fsys fs.FS, mainFile string) (target string, needsSetup bool) {
	base := mainDir(mainFile)
	var texs, bibs []string
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".refract") {
			return fs.SkipDir
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".tex":
			texs = append(texs, p)
		case ".bib":
			bibs = append(bibs, p)
		}
		return nil
	})
	// The main file first: its bibliography is the one that is printed.
	for i, t := range texs {
		if t == mainFile {
			texs[0], texs[i] = texs[i], texs[0]
		}
	}
	for _, t := range texs {
		b, err := fs.ReadFile(fsys, t)
		if err != nil {
			continue
		}
		for _, m := range bibCommand.FindAllStringSubmatch(projects.StripComments(string(b)), -1) {
			arg := m[2]
			if m[1] == "bibliography" {
				// LaTeX drops every space: \bibliography{my refs} loads myrefs.bib.
				arg = strings.Join(strings.Fields(arg), "")
			}
			// The first usable name of the list: \bibliography{a,b} loads both.
			for _, name := range strings.Split(arg, ",") {
				name = strings.TrimSpace(name)
				if name == "" || strings.HasPrefix(name, "/") {
					continue
				}
				if !strings.HasSuffix(strings.ToLower(name), ".bib") {
					name += ".bib"
				}
				// Paths in \bibliography are relative to the main file's folder
				// (Tectonic runs there), whichever .tex file names them.
				if p := path.Join(base, name); !outside(p) {
					return p, false
				}
			}
		}
	}
	if base != "." { // a main file at the root keeps the first .bib, as before
		for _, b := range bibs {
			if path.Dir(b) == base {
				return b, true
			}
		}
	}
	if len(bibs) > 0 {
		return bibs[0], true
	}
	return path.Join(base, "references.bib"), true
}

// mainDir is the folder Tectonic compiles mainFile in, relative to the
// project; "." for a main file at the root or one outside the project.
func mainDir(mainFile string) string {
	d := path.Dir(path.Clean(mainFile))
	if outside(d) {
		return "."
	}
	return d
}

// LoadName is how a .tex file names the project-relative bib to load it:
// relative to the main file's folder, where \bibliography and \addbibresource
// resolve. For example "references.bib" for paper/references.bib when the main
// file is paper/main.tex, or "../shared/lib.bib" for shared/lib.bib.
func LoadName(mainFile, bib string) string {
	rel, err := filepath.Rel(filepath.FromSlash(mainDir(mainFile)), filepath.FromSlash(bib))
	if err != nil {
		return bib
	}
	return filepath.ToSlash(rel)
}

// outside reports whether the clean path p leaves the project.
func outside(p string) bool {
	return p == ".." || strings.HasPrefix(p, "../") || strings.HasPrefix(p, "/")
}

// Citation is the outcome of Cite.
type Citation struct {
	Key        string `json:"key"`
	Path       string `json:"path"`        // the .bib file, relative to the project
	Added      bool   `json:"added"`       // false: the paper was already there
	NeedsSetup bool   `json:"needs_setup"` // no .tex file loads a bibliography yet
	Load       string `json:"load"`        // Path as \addbibresource names it (see LoadName)
}

// Cite adds p to the bibliography of the project at root (see BibTarget).
func Cite(root *os.Root, mainFile string, p Ref) (Citation, error) {
	target, needsSetup := BibTarget(root.FS(), mainFile)
	target, err := projects.CheckPath(target)
	if err != nil {
		return Citation{}, fmt.Errorf("the bibliography path in the project is not usable: %w", err)
	}
	old, err := root.ReadFile(target)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return Citation{}, err
	}
	content, key, added := AddEntry(string(old), p)
	c := Citation{Key: key, Path: target, Added: added, NeedsSetup: needsSetup, Load: LoadName(mainFile, target)}
	if !added {
		return c, nil
	}
	if d := path.Dir(target); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return c, err
		}
	}
	return c, root.WriteFile(target, []byte(content), 0o644)
}

var citeCmd = `\\(?:cite|citep|citet|parencite|textcite|autocite|footcite)\*?(?:\[[^\]]*\])?`

// OnlyAddsCites reports whether edited differs from original only by citing
// key: new \cite{key} commands (with the space or ~ before them) or key added
// to an existing \cite{…}. Anything else (a reworded sentence, a removed
// character) makes it false, as does adding no citation at all.
func OnlyAddsCites(original, edited, key string) bool {
	k := regexp.QuoteMeta(key)
	stripped := regexp.MustCompile(`[ ~]?`+citeCmd+`\{`+k+`\}`).ReplaceAllString(edited, "")
	// key inside an existing command: "{a,key}", "{key,a}", "{a, key, b}"
	stripped = regexp.MustCompile(`(`+citeCmd+`\{[^}]*?),\s*`+k+`([,}])`).ReplaceAllString(stripped, "$1$2")
	stripped = regexp.MustCompile(`(`+citeCmd+`\{)`+k+`,\s*`).ReplaceAllString(stripped, "$1")
	return stripped == original && edited != original
}

// AppendCite is the placement used when the model's is unusable: ~\cite{key}
// before the selection's final punctuation, else at its end.
func AppendCite(selection, key string) string {
	body := strings.TrimRight(selection, " \t\r\n")
	trail := selection[len(body):]
	cite := `~\cite{` + key + `}`
	if n := len(body); n > 0 && strings.ContainsRune(".;:!?", rune(body[n-1])) {
		return body[:n-1] + cite + body[n-1:] + trail
	}
	return body + cite + trail
}
