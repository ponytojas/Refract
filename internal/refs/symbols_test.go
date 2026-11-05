package refs

import (
	"encoding/json"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTexLabels(t *testing.T) {
	for name, c := range map[string]struct {
		src  string
		want []string
	}{
		"plain":                {`\section{A}\label{sec:a} text \label{eq:b}`, []string{"sec:a", "eq:b"}},
		"optional argument":    {`\label[type]{x}`, []string{"x"}},
		"comment":              {"% \\label{no}\n\\label{yes}", []string{"yes"}},
		"escaped percent":      {`100\% \label{yes}`, []string{"yes"}},
		"verbatim":             {"\\begin{verbatim}\\label{no}\\end{verbatim}\\label{yes}", []string{"yes"}},
		"percent in verbatim":  {"\\begin{verbatim}\n50% \\end{verbatim}\n\\label{yes}", []string{"yes"}},
		"commented verbatim":   {"% \\begin{verbatim}\n\\label{yes}", []string{"yes"}},
		"unterminated literal": {"\\label{a}\\begin{lstlisting}\\label{no}", []string{"a"}},
		"line break":           {`\\label{no}`, nil},
		"macro argument":       {`\newcommand{\l}[1]{\label{#1}}`, nil},
		"computed":             {`\label{\name}`, nil},
		"blank":                {`\label{ }`, nil},
	} {
		if got := texLabels(c.src); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, want %q", name, got, c.want)
		}
	}
}

func TestBibEntries(t *testing.T) {
	bib := `@string{jacm = "J. ACM"}
@Comment{note, not an entry}
@PREAMBLE{x, "y"}
@article{knuth1984,
  title = {Literate {P}rogramming, or how},
  author = "Knuth, Donald E.",
  year = 1984,
}
@Book { lamport94 ,
  Title = "{\LaTeX}: a document preparation system",
  Author = {Leslie Lamport and Mittelbach, Frank},
  abstract = {title = wrong, year = 1},
  date = {1994-05-01} }
@misc{many, author = {A One and B Two and C Three}}
@misc{others, author = {G{\"o}del, Kurt and others}, title = {Caf\'e \& co}}
@misc{bare,}
@misc{unclosed, title = {Never closed
@misc{after, title = {Still read}}`
	want := []BibEntry{
		{Key: "knuth1984", Title: "Literate Programming, or how", Author: "Knuth", Year: "1984"},
		{Key: "lamport94", Title: `\LaTeX: a document preparation system`, Author: "Lamport, Mittelbach", Year: "1994"},
		{Key: "many", Author: "One et al."},
		{Key: "others", Title: "Cafe & co", Author: "Godel et al."},
		{Key: "bare"},
		{Key: "unclosed"},
		{Key: "after", Title: "Still read"},
	}
	if got := BibEntries(bib); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
}

func TestScanSymbols(t *testing.T) {
	file := func(s string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(s)} }
	got := ScanSymbols(fstest.MapFS{
		"main.tex":             file(`\label{b}\label{a}`),
		"sec/intro.TEX":        file(`\label{a}\label{c}`),
		"refs.bib":             file(`@book{k2, title={x}}`),
		"more/other.bib":       file(`@book{k1, title={x}} @book{k2, title={x}}`),
		"fig/logo.png":         file(`\label{not-tex}`),
		"huge.tex":             file(`\label{huge}` + strings.Repeat(" ", maxSymbolFile)),
		".refract/build/a.tex": file(`\label{built}`),
		".git/config.tex":      file(`\label{git}`),
		"link.tex":             {Data: []byte(`\label{link}`), Mode: fs.ModeSymlink},
	})
	want := Symbols{
		Labels:    []string{"a", "b", "c"},
		Citations: []BibEntry{{Key: "k1", Title: "x"}, {Key: "k2", Title: "x"}},
		Files:     []string{"fig/logo.png", "huge.tex", "main.tex", "more/other.bib", "refs.bib", "sec/intro.TEX"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestScanSymbolsEmptyIsNotNull(t *testing.T) {
	b, err := json.Marshal(ScanSymbols(fstest.MapFS{}))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(b), `{"labels":[],"citations":[],"files":[]}`; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}
