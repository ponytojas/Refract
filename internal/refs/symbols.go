package refs

import (
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
)

// Symbols is what the editor completes from: every \label, every BibTeX entry
// and every file of a project, each sorted and without duplicates.
type Symbols struct {
	Labels    []string   `json:"labels"`
	Citations []BibEntry `json:"citations"`
	Files     []string   `json:"files"`
}

// BibEntry is a bibliography entry, with what helps recognise a work by its key.
type BibEntry struct {
	Key    string `json:"key"`
	Title  string `json:"title,omitempty"`
	Author string `json:"author,omitempty"` // short form: "Knuth", "Knuth, Lamport", "Knuth et al."
	Year   string `json:"year,omitempty"`
}

// maxSymbolFile bounds what is read per file: larger ones are listed but not scanned.
const maxSymbolFile = 8 << 20

// ScanSymbols reads a project's symbols. It is best effort: completion should
// still offer what it can when a file can't be read.
func ScanSymbols(fsys fs.FS) Symbols {
	s := Symbols{Labels: []string{}, Citations: []BibEntry{}, Files: []string{}}
	fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".refract") {
			return fs.SkipDir
		}
		// Regular files only: reading a named pipe would block forever.
		if !d.Type().IsRegular() {
			return nil
		}
		s.Files = append(s.Files, p)
		ext := strings.ToLower(path.Ext(p))
		if ext != ".tex" && ext != ".bib" {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSymbolFile {
			return nil
		}
		b, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		if ext == ".tex" {
			s.Labels = append(s.Labels, texLabels(string(b))...)
		} else {
			s.Citations = append(s.Citations, BibEntries(string(b))...)
		}
		return nil
	})
	for _, list := range []*[]string{&s.Labels, &s.Files} {
		slices.Sort(*list)
		*list = slices.Compact(*list)
	}
	byKey := func(a, b BibEntry) int { return strings.Compare(a.Key, b.Key) }
	slices.SortStableFunc(s.Citations, byKey)
	s.Citations = slices.CompactFunc(s.Citations, func(a, b BibEntry) bool { return a.Key == b.Key })
	return s
}

// BibEntries lists the citable entries of a .bib file's content.
func BibEntries(bib string) []BibEntry {
	var entries []BibEntry
	found := entryKey.FindAllStringSubmatchIndex(bib, -1)
	for i, m := range found {
		kind, _, _ := strings.Cut(bib[m[0]+1:m[1]], "{")
		switch strings.ToLower(strings.TrimSpace(kind)) {
		case "comment", "string", "preamble":
			continue
		}
		// Up to the next entry at most, so an unclosed brace can't reach into it.
		end := len(bib)
		if i+1 < len(found) {
			end = found[i+1][0]
		}
		fields := bibFields(bib[m[1]:end])
		year := fields["year"]
		if year == "" && len(fields["date"]) >= 4 { // biblatex: 2024-05-01
			year = fields["date"][:4]
		}
		entries = append(entries, BibEntry{
			Key:    bib[m[2]:m[3]],
			Title:  fields["title"],
			Author: shortAuthors(fields["author"]),
			Year:   year,
		})
	}
	return entries
}

// bibFields reads the name = value pairs of an entry's body, up to its closing
// brace. A comma or brace only counts outside the value's own braces and quotes.
func bibFields(body string) map[string]string {
	fields := map[string]string{}
	depth, quoted, start := 0, false, 0
	add := func(end int) {
		name, value, ok := strings.Cut(body[start:end], "=")
		if ok {
			fields[strings.ToLower(strings.TrimSpace(name))] = bibText(value)
		}
		start = end + 1
	}
	for i := 0; i < len(body); i++ {
		switch c := body[i]; {
		case c == '\\':
			i++
		case c == '"' && depth == 0:
			quoted = !quoted
		case c == '{':
			depth++
		case c == '}' && depth > 0:
			depth--
		case c == '}' && !quoted:
			add(i)
			return fields
		case c == ',' && depth == 0 && !quoted:
			add(i)
		}
	}
	return fields
}

// accent matches LaTeX accent commands, whose letter stands well enough alone in a hint.
var accent = regexp.MustCompile(`\\['"^~=.` + "`" + `]`)

// bibText turns a BibTeX value into plain text.
func bibText(value string) string {
	s := accent.ReplaceAllString(value, "")
	s = strings.NewReplacer("{", "", "}", "", `"`, "", `\&`, "&", `\%`, "%", `\_`, "_", "~", " ").Replace(s)
	return strings.Join(strings.Fields(s), " ")
}

// shortAuthors reduces "Knuth, Donald E. and Leslie Lamport" to its surnames.
func shortAuthors(authors string) string {
	if authors == "" {
		return ""
	}
	names := strings.Split(authors, " and ")
	surname := func(name string) string {
		if last, _, ok := strings.Cut(name, ","); ok {
			return strings.TrimSpace(last)
		}
		parts := strings.Fields(name)
		if len(parts) == 0 {
			return ""
		}
		return parts[len(parts)-1]
	}
	switch len(names) {
	case 1:
		return surname(names[0])
	case 2:
		if strings.TrimSpace(names[1]) == "others" {
			return surname(names[0]) + " et al."
		}
		return surname(names[0]) + ", " + surname(names[1])
	}
	return surname(names[0]) + " et al."
}

// texToken walks LaTeX source left to right, so comments and literal
// environments hide each other correctly: `50% \end{verbatim}` doesn't end a
// verbatim block, and `% \begin{verbatim}` doesn't start one. The last
// alternative swallows escapes such as \% and the line break in \\label.
var texToken = regexp.MustCompile(`(?s)%[^\n]*|\\begin\{(verbatim\*?|Verbatim\*?|lstlisting|minted|comment)\}|\\label(?:\[[^\]]*\])?\{([^{}\\#\n]+)\}|\\.`)

// texLabels lists the \label names in LaTeX source. Names built by a macro
// (\label{#1}, \label{\name}) aren't known until compiling, so they are skipped.
func texLabels(src string) []string {
	var labels []string
	for i := 0; i < len(src); {
		m := texToken.FindStringSubmatchIndex(src[i:])
		if m == nil {
			break
		}
		end := i + m[1]
		switch {
		case m[2] >= 0: // literal environment: resume after its \end
			closing := `\end{` + src[i+m[2]:i+m[3]] + `}`
			at := strings.Index(src[end:], closing)
			if at < 0 {
				return labels
			}
			end += at + len(closing)
		case m[4] >= 0:
			if name := strings.TrimSpace(src[i+m[4] : i+m[5]]); name != "" {
				labels = append(labels, name)
			}
		}
		i = end
	}
	return labels
}
