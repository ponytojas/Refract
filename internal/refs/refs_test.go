package refs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func ref(doi, eprint string) Ref {
	return Ref{Title: "Attention Is All You Need", Authors: []string{"Ashish Vaswani", "Noam Shazeer"}, Year: 2017, DOI: doi, Eprint: eprint,
		BibTeX: func(key string) string {
			return "@misc{" + key + ",\n  eprint = {" + eprint + "},\n  doi = {" + doi + "},\n}\n"
		}}
}

func TestKeyAndEscape(t *testing.T) {
	if k := Key(ref("", "1706.03762")); k != "vaswani2017attention" {
		t.Errorf("key = %q", k)
	}
	if k := Key(Ref{Authors: []string{"José Ñúñez"}, Title: "On the Theory", Year: 2020}); k != "nunez2020theory" {
		t.Errorf("accented key = %q", k)
	}
	if got := Escape(`R&D at 50% & more, \& kept`); got != `R\&D at 50\% \& more, \& kept` {
		t.Errorf("escape = %q", got)
	}
}

func TestAddEntry(t *testing.T) {
	p := ref("", "1706.03762")
	bib, key, added := AddEntry("", p)
	if !added || key != "vaswani2017attention" || !strings.HasPrefix(bib, "@misc{vaswani2017attention,") {
		t.Fatalf("first add = %q %v\n%s", key, added, bib)
	}
	// Same paper again (any version): not duplicated, existing key returned.
	if _, k, added := AddEntry(bib+"@book{other, title={X}}\n", p); added || k != key {
		t.Errorf("re-add = %q %v", k, added)
	}
	// Same DOI written as a URL, under another key.
	byDOI := "@article{lecun15,\n  doi = {https://doi.org/10.1038/Nature14539},\n}\n"
	if _, k, added := AddEntry(byDOI, ref("10.1038/nature14539", "")); added || k != "lecun15" {
		t.Errorf("DOI dedupe = %q %v", k, added)
	}
	// Key taken by another work: suffix.
	taken := "@article{vaswani2017attention,\n  title = {Something else},\n}"
	out, k, added := AddEntry(taken, p)
	if !added || k != "vaswani2017attentiona" || !strings.HasPrefix(out, taken+"\n\n@misc{vaswani2017attentiona,") {
		t.Errorf("collision = %q\n%s", k, out)
	}
}

func TestBibTarget(t *testing.T) {
	tex := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	for name, c := range map[string]struct {
		main       string
		files      fstest.MapFS
		want       string
		needsSetup bool
	}{
		"bibliography in main": {"main.tex", fstest.MapFS{"main.tex": tex(`\bibliography{refs,extra}`), "other.bib": {}}, "refs.bib", false},
		"biblatex resource":    {"main.tex", fstest.MapFS{"main.tex": tex(`\addbibresource[label=x]{bib/library.bib}`)}, "bib/library.bib", false},
		"main file first":      {"main.tex", fstest.MapFS{"a.tex": tex(`\bibliography{a}`), "main.tex": tex(`\bibliography{m}`)}, "m.bib", false},
		"only a .bib":          {"main.tex", fstest.MapFS{"main.tex": tex(`x`), "lit.bib": {}}, "lit.bib", true},
		"nothing yet":          {"main.tex", fstest.MapFS{"main.tex": tex(`x`)}, "references.bib", true},
		// Tectonic runs in the main file's folder: names are relative to it.
		"main in a subfolder":           {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\bibliography{references}`)}, "paper/references.bib", false},
		"resource in subfolder":         {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\addbibresource{refs.bib}`)}, "paper/refs.bib", false},
		"up but inside":                 {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\bibliography{../shared/lib}`)}, "shared/lib.bib", false},
		"outside the project":           {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\bibliography{../../x}`)}, "paper/references.bib", true},
		"absolute path":                 {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\bibliography{/etc/x}`)}, "paper/references.bib", true},
		"in an included file":           {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\input{chapters/intro}`), "paper/chapters/intro.tex": tex(`\bibliography{refs}`)}, "paper/refs.bib", false},
		"subfolder, nothing":            {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`x`)}, "paper/references.bib", true},
		"bib next to main first":        {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`x`), "a/lit.bib": {}, "paper/mine.bib": {}}, "paper/mine.bib", true},
		"commented-out command":         {"main.tex", fstest.MapFS{"main.tex": tex("% \\bibliography{old}\n\\bibliography{new}")}, "new.bib", false},
		"only commented out":            {"main.tex", fstest.MapFS{"main.tex": tex(`x % \bibliography{old}`), "lit.bib": {}}, "lit.bib", true},
		"escaped percent is no comment": {"main.tex", fstest.MapFS{"main.tex": tex(`50\% \bibliography{refs}`)}, "refs.bib", false},
		"first usable name of a list":   {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\bibliography{../../x, refs}`)}, "paper/refs.bib", false},
		"empty first name":              {"main.tex", fstest.MapFS{"main.tex": tex(`\bibliography{, refs}`)}, "refs.bib", false},
		"spaces in \\bibliography":      {"paper/main.tex", fstest.MapFS{"paper/main.tex": tex(`\bibliography{my refs, other}`)}, "paper/myrefs.bib", false},
		"spaces in \\addbibresource":    {"main.tex", fstest.MapFS{"main.tex": tex(`\addbibresource{my refs.bib}`)}, "my refs.bib", false},
		"root main keeps first .bib":    {"main.tex", fstest.MapFS{"main.tex": tex(`x`), "a/first.bib": {}, "z.bib": {}}, "a/first.bib", true},
		"main outside the project":      {"../main.tex", fstest.MapFS{}, "references.bib", true},
	} {
		got, setup := BibTarget(c.files, c.main)
		if got != c.want || setup != c.needsSetup {
			t.Errorf("%s: %q %v, want %q %v", name, got, setup, c.want, c.needsSetup)
		}
	}
}

func TestCiteMainInSubfolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "paper"), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := "@book{other, title={X}}\n"
	for name, data := range map[string]string{"paper/main.tex": `\bibliography{references}`, "paper/references.bib": existing} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	p := ref("", "1706.03762")
	c, err := Cite(root, "paper/main.tex", p)
	if err != nil || !c.Added || c.Path != "paper/references.bib" || c.Load != "references.bib" || c.NeedsSetup {
		t.Fatalf("first cite = %+v, %v", c, err)
	}
	if c, err := Cite(root, "paper/main.tex", p); err != nil || c.Added {
		t.Fatalf("second cite = %+v, %v", c, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "paper", "references.bib"))
	if !strings.HasPrefix(string(b), existing) || strings.Count(string(b), "vaswani2017attention,") != 1 {
		t.Errorf("paper/references.bib =\n%s", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "references.bib")); !os.IsNotExist(err) {
		t.Errorf("a references.bib was created at the root (err %v)", err)
	}
}

func TestLoadName(t *testing.T) {
	for _, c := range []struct{ main, bib, want string }{
		{"main.tex", "references.bib", "references.bib"},
		{"main.tex", "bib/library.bib", "bib/library.bib"},
		{"paper/main.tex", "paper/references.bib", "references.bib"},
		{"paper/main.tex", "shared/lib.bib", "../shared/lib.bib"},
		{"a/b/main.tex", "a/refs.bib", "../refs.bib"},
		{"../main.tex", "references.bib", "references.bib"},
	} {
		if got := LoadName(c.main, c.bib); got != c.want {
			t.Errorf("LoadName(%q, %q) = %q, want %q", c.main, c.bib, got, c.want)
		}
	}
}

func TestCiteNeedsSetupInSubfolder(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "paper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "paper", "main.tex"), []byte(`x`), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	// The setup hint must name the file as Tectonic resolves it from paper/.
	c, err := Cite(root, "paper/main.tex", ref("", "1706.03762"))
	if err != nil || c.Path != "paper/references.bib" || c.Load != "references.bib" || !c.NeedsSetup {
		t.Fatalf("cite = %+v, %v", c, err)
	}
}

func TestOnlyAddsCites(t *testing.T) {
	orig := `Transformers rely on attention. They scale well \cite{kaplan2020}.`
	for edited, want := range map[string]bool{
		`Transformers rely on attention~\cite{vaswani}. They scale well \cite{kaplan2020}.`:       true,
		`Transformers rely on attention \citep{vaswani}. They scale well \cite{kaplan2020}.`:      true,
		`Transformers rely on attention. They scale well \cite{kaplan2020,vaswani}.`:              true,
		`Transformers rely on attention. They scale well \cite{vaswani, kaplan2020}.`:             true,
		`Transformers rely on attention~\cite[p.~3]{vaswani}. They scale well \cite{kaplan2020}.`: true,
		orig: false, // nothing added
		`Transformers rely heavily on attention~\cite{vaswani}. They scale well \cite{kaplan2020}.`: false, // reworded
		`Transformers rely on attention~\cite{other}. They scale well \cite{kaplan2020}.`:           false, // another key
		`Transformers rely on attention~\cite{vaswani}. They scale well.`:                           false, // removed a citation
	} {
		if got := OnlyAddsCites(orig, edited, "vaswani"); got != want {
			t.Errorf("OnlyAddsCites(%q) = %v, want %v", edited, got, want)
		}
	}
}

func TestAppendCite(t *testing.T) {
	for sel, want := range map[string]string{
		"They scale well.\n": "They scale well~\\cite{k}.\n",
		"a claim":            "a claim~\\cite{k}",
		"  ends?  ":          "  ends~\\cite{k}?  ",
	} {
		if got := AppendCite(sel, "k"); got != want {
			t.Errorf("AppendCite(%q) = %q, want %q", sel, got, want)
		}
	}
}
