package compile

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFontSourcePreservesSourcesAndInternalAssets(t *testing.T) {
	dir := t.TempDir()
	source := "\\documentclass{article}\n\\begin{document}Hello\\end{document}\n"
	write(t, dir, "nested/main.tex", source)
	write(t, dir, "styles/shared.sty", "original style")
	write(t, dir, ".refract/fonts/family/regular.ttf", "prepared font")
	write(t, dir, ".refract/private.txt", "private state")
	write(t, dir, ".git/config", "git state")
	stage, err := fontSource(dir, "nested/main.tex", "\\relax\n\\relax\n")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	got, _ := os.ReadFile(filepath.Join(stage, "nested/main.tex"))
	if strings.Count(string(got), "\n") != strings.Count(source, "\n") || !strings.HasSuffix(string(got), source) {
		t.Fatalf("source lines changed: %q", got)
	}
	original, _ := os.ReadFile(filepath.Join(dir, "nested/main.tex"))
	if string(original) != source {
		t.Fatal("original document was changed")
	}
	font, err := os.ReadFile(filepath.Join(stage, ".refract/fonts/family/regular.ttf"))
	if err != nil || string(font) != "prepared font" {
		t.Fatalf("font not staged: %q %v", font, err)
	}
	for _, p := range []string{".git/config", ".refract/private.txt"} {
		if _, err := os.Stat(filepath.Join(stage, p)); !os.IsNotExist(err) {
			t.Fatalf("unexpected internal file %s", p)
		}
	}
	// Atomic editor saves must not change the snapshot being compiled.
	write(t, dir, "styles/new.sty", "updated style")
	if err := os.Rename(filepath.Join(dir, "styles/new.sty"), filepath.Join(dir, "styles/shared.sty")); err != nil {
		t.Fatal(err)
	}
	got, _ = os.ReadFile(filepath.Join(stage, "styles/shared.sty"))
	if string(got) != "original style" {
		t.Fatal("source snapshot changed during compilation")
	}
}

func TestRestoreSyncPaths(t *testing.T) {
	dir := t.TempDir()
	stage := filepath.Join(dir, ".refract/compile-one")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "main.synctex.gz")
	f, err := os.Create(file)
	if err != nil {
		t.Fatal(err)
	}
	z := gzip.NewWriter(f)
	_, err = io.WriteString(z, "SyncTeX Version:1\nInput:1:"+filepath.Join(stage, "sub/main.tex")+"\nInput:2:/bundle/article.cls\nContent:\n")
	if err != nil {
		t.Fatal(err)
	}
	z.Close()
	f.Close()
	if err := restoreSyncPaths(file, stage, dir); err != nil {
		t.Fatal(err)
	}
	f, err = os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	r, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	physical, _ := filepath.EvalSymlinks(dir)
	if !strings.Contains(string(b), "Input:1:"+filepath.Join(physical, "sub/main.tex")) || !strings.Contains(string(b), "Input:2:/bundle/article.cls") || strings.Contains(string(b), "compile-one") {
		t.Fatalf("incorrect SyncTeX paths: %s", b)
	}
}

func TestFontSourceStripsOnlyLeadingBOM(t *testing.T) {
	dir := t.TempDir()
	source := "\ufeff\\documentclass{article}\n% Embedded BOM stays: \ufeff\n"
	write(t, dir, "main.tex", source)
	if err := os.MkdirAll(filepath.Join(dir, ".refract"), 0o700); err != nil {
		t.Fatal(err)
	}
	stage, err := fontSource(dir, "main.tex", "\\relax\n")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(stage)
	b, err := os.ReadFile(filepath.Join(stage, "main.tex"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(b), strings.TrimPrefix(source, "\ufeff")) || strings.Count(string(b), "\ufeff") != 1 {
		t.Fatalf("staged source must remove only the leading BOM: %q", b)
	}
	if strings.Count(string(b), "\n") != strings.Count(source, "\n") {
		t.Fatal("source line numbers changed")
	}
	original, err := os.ReadFile(filepath.Join(dir, "main.tex"))
	if err != nil || string(original) != source {
		t.Fatal("original source was changed")
	}
}
