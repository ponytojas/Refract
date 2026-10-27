package projects

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io/fs"
	"mime/multipart"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

type entry struct {
	name, body string
	symlink    bool
}

func makeZip(t *testing.T, entries ...entry) *zip.Reader {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.symlink {
			h.SetMode(os.ModeSymlink | 0o777)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(e.body))
	}
	zw.Close()
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr
}

func newService(t *testing.T) *Service {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: DefaultLimits}
}

// tree returns path → content of a project's working files.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := walkFiles(dir, func(rel string, _ fs.FileInfo) error {
		b, err := os.ReadFile(filepath.Join(dir, rel))
		out[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertNothingLeft(t *testing.T, s *Service) {
	t.Helper()
	if ps, _ := s.List(context.Background()); len(ps) != 0 {
		t.Fatalf("projects registered: %v", ps)
	}
	if ents, _ := os.ReadDir(s.Dir); len(ents) != 0 {
		t.Fatalf("leftovers in %s: %v", s.Dir, ents)
	}
}

const doc = "\\documentclass{article}\n\\begin{document}\nHi\n\\end{document}\n"

func TestImportRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"../evil.tex", "/abs.tex", "a/../../evil.tex", "C:/evil.tex", `..\evil.tex`} {
		t.Run(name, func(t *testing.T) {
			s := newService(t)
			_, err := s.Import(context.Background(), "x", ZipSource(makeZip(t, entry{name: "main.tex", body: doc}, entry{name: name, body: "x"})))
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("err = %v, want ErrUnsafePath", err)
			}
			assertNothingLeft(t, s)
			if _, err := os.Stat(filepath.Join(filepath.Dir(s.Dir), "evil.tex")); err == nil {
				t.Fatal("file written outside the project")
			}
		})
	}
}

func TestImportLimits(t *testing.T) {
	cases := map[string][]entry{
		"too many files": {{name: "a.tex", body: "a"}, {name: "b.tex", body: "b"}, {name: "c.tex", body: "c"}},
		"too many bytes": {{name: "a.tex", body: strings.Repeat("x", 60)}, {name: "b.tex", body: strings.Repeat("y", 60)}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			s := newService(t)
			s.Limits = Limits{MaxBytes: 100, MaxFiles: 2}
			_, err := s.Import(context.Background(), "x", ZipSource(makeZip(t, entries...)))
			if !errors.Is(err, ErrTooLarge) {
				t.Fatalf("err = %v, want ErrTooLarge", err)
			}
			assertNothingLeft(t, s)
		})
	}
}

func TestImportSkipsAndStrips(t *testing.T) {
	s := newService(t)
	res, err := s.Import(context.Background(), "Paper", ZipSource(makeZip(t,
		entry{name: "paper/main.tex", body: doc},
		entry{name: "paper/sec/intro.tex", body: "Intro"},
		entry{name: "paper/link.tex", body: "/etc/passwd", symlink: true},
		entry{name: "paper/.git/hooks/post-commit", body: "#!/bin/sh\nrm -rf /"},
		entry{name: "__MACOSX/paper/._main.tex", body: "junk"},
		entry{name: "paper/.DS_Store", body: "junk"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	got := tree(t, s.Path(res.Project.ID))
	want := map[string]string{"main.tex": doc, "sec/intro.tex": "Intro"}
	if len(got) != len(want) || got["main.tex"] != doc || got["sec/intro.tex"] != "Intro" {
		t.Fatalf("tree = %v, want %v", got, want)
	}
	if len(res.Skipped) != 1 || !strings.Contains(res.Skipped[0], ".git/") {
		t.Fatalf("skipped = %v, want the .git entry reported", res.Skipped)
	}
	if hook, _ := os.ReadFile(filepath.Join(s.Path(res.Project.ID), ".git/hooks/post-commit")); strings.Contains(string(hook), "rm -rf") {
		t.Fatal("planted git hook was written")
	}
}

func TestImportCreatesRepoAndDetectsMain(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	res, err := s.Import(ctx, "Thesis", ZipSource(makeZip(t,
		entry{name: "thesis.tex", body: doc + "\\setmainfont{Open Sans}\n"},
		entry{name: "chapters/one.tex", body: "One"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	p := res.Project
	if p.MainFile != "thesis.tex" || res.Candidates != nil {
		t.Fatalf("main = %q, candidates = %v", p.MainFile, res.Candidates)
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings = %+v", res.Warnings)
	}
	dir := s.Path(p.ID)
	if out, err := git(ctx, dir, "log", "--oneline"); err != nil || strings.Count(out, "\n") != 1 {
		t.Fatalf("git log = %q, %v", out, err)
	}
	// Build output must never show up as a change.
	os.MkdirAll(filepath.Join(dir, ".refract/build"), 0o755)
	os.WriteFile(filepath.Join(dir, ".refract/build/main.pdf"), []byte("pdf"), 0o644)
	if out, _ := git(ctx, dir, "status", "--porcelain"); out != "" {
		t.Fatalf("working tree dirty: %q", out)
	}

	if err := s.SetMainFile(ctx, p.ID, "../outside.tex"); err == nil {
		t.Fatal("main file outside the project accepted")
	}
	if err := s.SetMainFile(ctx, p.ID, "chapters/one.tex"); err != nil {
		t.Fatal(err)
	}
	if err := s.Rename(ctx, p.ID, "My thesis"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.Name != "My thesis" || got.MainFile != "chapters/one.tex" {
		t.Fatalf("project = %+v", got)
	}
	if err := s.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("project directory not removed")
	}
	if _, err := s.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete: %v", err)
	}
}

func TestCreateEmpty(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	p, err := s.Create(ctx, "  ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "Untitled" {
		t.Fatalf("name = %q", p.Name)
	}
	if out, err := git(ctx, s.Path(p.ID), "log", "--oneline"); err != nil || out == "" {
		t.Fatalf("no initial commit: %q %v", out, err)
	}
}

func TestExportReimportIsIdentical(t *testing.T) {
	ctx := context.Background()
	s := newService(t)
	res, err := s.Import(ctx, "p", ZipSource(makeZip(t,
		entry{name: "main.tex", body: doc},
		entry{name: "fig/plot.pdf", body: "%PDF-binary\x00\x01"},
		entry{name: "refs.bib", body: "@article{x,}"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(s.Path(res.Project.ID), ".refract/build"), 0o755)
	os.WriteFile(filepath.Join(s.Path(res.Project.ID), ".refract/build/main.pdf"), []byte("x"), 0o644)

	var buf bytes.Buffer
	if err := s.Export(ctx, res.Project.ID, &buf); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if strings.HasPrefix(f.Name, ".git") || strings.HasPrefix(f.Name, ".refract") {
			t.Fatalf("export contains %s", f.Name)
		}
	}
	again, err := s.Import(ctx, "p2", ZipSource(zr))
	if err != nil {
		t.Fatal(err)
	}
	a, b := tree(t, s.Path(res.Project.ID)), tree(t, s.Path(again.Project.ID))
	delete(a, ".refract/build/main.pdf") // walkFiles already skips it; keep the intent explicit
	if len(a) != len(b) {
		t.Fatalf("trees differ:\n%v\n%v", a, b)
	}
	for k, v := range a {
		if b[k] != v {
			t.Fatalf("%s differs after re-import", k)
		}
	}
}

func TestMultipartSourceStripsFolder(t *testing.T) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for name, body := range map[string]string{"paper/main.tex": doc, "paper/sec/a.tex": "A"} {
		w, _ := mw.CreateFormFile("file", name)
		w.Write([]byte(body))
	}
	mw.Close()
	s := newService(t)
	res, err := s.Import(context.Background(), "paper", MultipartSource(multipart.NewReader(&buf, mw.Boundary())))
	if err != nil {
		t.Fatal(err)
	}
	got := tree(t, s.Path(res.Project.ID))
	if got["main.tex"] != doc || got["sec/a.tex"] != "A" || len(got) != 2 {
		t.Fatalf("tree = %v", got)
	}
}

func TestDetectMain(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"main.tex wins", map[string]string{"a.tex": doc, "main.tex": doc}, "main.tex"},
		{"main.tex without class loses", map[string]string{"main.tex": "x", "paper.tex": doc}, "paper.tex"},
		{"shallowest", map[string]string{"z/deep.tex": doc, "top.tex": doc}, "top.tex"},
		{"alphabetical tie", map[string]string{"b.tex": doc, "a.tex": doc}, "a.tex"},
		{"commented class ignored", map[string]string{"a.tex": "% \\documentclass{article}"}, ""},
		{"none", map[string]string{"notes.tex": "x"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, body := range c.files {
				os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755)
				os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
			}
			got, candidates, err := detectMain(dir)
			if err != nil || got != c.want || len(candidates) != len(c.files) {
				t.Fatalf("got %q %v %v, want %q", got, candidates, err, c.want)
			}
		})
	}
}

func TestCheckSource(t *testing.T) {
	cases := []struct {
		name, src string
		want      []string // kinds
	}{
		{"font exists", `\IfFontExistsTF{Open Sans}{a}{b}`, nil},
		{"system font", `\setmainfont{Open Sans}`, nil},
		{"system font with opts", `\setsansfont[Scale=0.9]{Open Sans}`, nil},
		{"newfontfamily", `\newfontfamily\headfont{Inter}[Scale=1]`, nil},
		{"path ok", "\\setmainfont{OpenSans}[\n  Path = fonts/,\n  Extension = .ttf,\n  UprightFont = *-Regular]", nil},
		{"path folder missing", "\\setmainfont{Inter}[Path = typefaces/, Extension = .otf]", []string{"missing-font"}},
		{"path font file missing", "\\setmainfont{OpenSans}[Path = fonts/, Extension = .ttf, UprightFont = *-Light]", []string{"missing-font"}},
		{"absolute path", "\\setmainfont{X}[Path = /Library/Fonts/, Extension = .ttf]", []string{"missing-font"}},
		{"path before ok", `\setmainfont[Path=fonts/]{OpenSans-Regular}`, nil},
		{"file name ok", `\setmonofont{lmmono10-regular.otf}`, nil},
		{"biblatex biber", `\usepackage[style=apa]{biblatex}`, []string{"biber"}},
		{"biblatex bibtex ok", `\usepackage[backend = bibtex, style=apa]{biblatex}`, nil},
		{"minted", `\usepackage{amsmath, minted}`, []string{"shell-escape"}},
		{"svg", `\RequirePackage{svg}`, []string{"shell-escape"}},
		{"commented out", `% \setmainfont{Open Sans}`, nil},
		{"escaped percent is not a comment", `50\% \setmainfont{Open Sans}`, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			exists := func(p string) bool { return p == "fonts" || p == "fonts/OpenSans-Regular.ttf" }
			ws := checkSource("main.tex", c.src, exists)
			var kinds []string
			for _, w := range ws {
				kinds = append(kinds, w.Kind)
			}
			if strings.Join(kinds, ",") != strings.Join(c.want, ",") {
				t.Fatalf("kinds = %v, want %v", kinds, c.want)
			}
		})
	}
}

// TestFixtures imports every private Prism export in fixtures/ (gitignored).
func TestFixtures(t *testing.T) {
	zips, _ := filepath.Glob("../../fixtures/*.zip")
	if len(zips) == 0 {
		t.Skip("no fixtures/*.zip")
	}
	s := newService(t)
	for _, z := range zips {
		t.Run(filepath.Base(z), func(t *testing.T) {
			res, err := s.ImportZip(context.Background(), filepath.Base(z), z)
			if err != nil {
				t.Fatal(err)
			}
			if res.Project.MainFile == "" {
				t.Errorf("main file not detected; candidates %v", res.Candidates)
			}
			for _, w := range res.Warnings {
				t.Logf("%s:%d %s", w.File, w.Line, w.Kind)
			}
		})
	}
}

// Users only ever see their own projects; code without a user (startup,
// background jobs) sees all of them.
func TestProjectsArePerUser(t *testing.T) {
	s := newService(t)
	s.Key = make([]byte, 32)
	ana := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	ben := auth.WithUser(context.Background(), auth.User{ID: "ben"})
	p, err := s.Create(ana, "thesis")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Get(ana, p.ID); err != nil || got.Owner != "ana" {
		t.Fatalf("owner get = %+v, %v", got, err)
	}
	notFound := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrNotFound) {
			t.Errorf("%s by another user: %v, want ErrNotFound", name, err)
		}
	}
	_, err = s.Get(ben, p.ID)
	notFound("get", err)
	notFound("rename", s.Rename(ben, p.ID, "x"))
	notFound("set main", s.SetMainFile(ben, p.ID, "main.tex"))
	_, err = s.ListFiles(ben, p.ID)
	notFound("list files", err)
	_, _, err = s.ReadFile(ben, p.ID, "main.tex")
	notFound("read file", err)
	notFound("write file", s.WriteFile(ben, p.ID, "x.tex", strings.NewReader("x")))
	_, err = s.History(ben, p.ID, 10)
	notFound("history", err)
	_, err = s.Remote(ben, p.ID)
	notFound("remote", err)
	notFound("delete", s.Delete(ben, p.ID))

	if ps, _ := s.List(ben); len(ps) != 0 {
		t.Errorf("ben lists %d projects", len(ps))
	}
	if ps, _ := s.List(ana); len(ps) != 1 {
		t.Errorf("ana lists %d projects", len(ps))
	}
	if ps, _ := s.List(context.Background()); len(ps) != 1 {
		t.Errorf("background lists %d projects", len(ps))
	}
	if _, err := s.Get(ana, p.ID); err != nil {
		t.Errorf("ben's attempts changed ana's project: %v", err)
	}
}
