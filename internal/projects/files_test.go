package projects

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newProject(t *testing.T) (*Service, string) {
	t.Helper()
	s := newService(t)
	res, err := s.Import(ownerCtx, "p", ZipSource(makeZip(t,
		entry{name: "main.tex", body: doc},
		entry{name: "sec/intro.tex", body: "Intro"},
		entry{name: "fig/logo.png", body: "\x89PNG\r\n\x1a\n\x00\x00"},
	)))
	if err != nil {
		t.Fatal(err)
	}
	return s, res.Project.ID
}

func TestFilePathsAreConfined(t *testing.T) {
	ctx := ownerCtx
	s, id := newProject(t)
	bad := []string{"", ".", "../x.tex", "/etc/passwd", "sec/../../x", ".git/config", "sub/.git/config", ".refract/build/main.pdf", ".DS_Store"}
	for _, p := range bad {
		if err := s.WriteFile(ctx, id, p, strings.NewReader("x")); !errors.Is(err, ErrBadPath) {
			t.Errorf("WriteFile(%q) err = %v, want ErrBadPath", p, err)
		}
		if _, _, err := s.ReadFile(ctx, id, p); !errors.Is(err, ErrBadPath) {
			t.Errorf("ReadFile(%q) err = %v, want ErrBadPath", p, err)
		}
		if err := s.Remove(ctx, id, p); !errors.Is(err, ErrBadPath) {
			t.Errorf("Remove(%q) err = %v, want ErrBadPath", p, err)
		}
		if err := s.Move(ctx, id, "main.tex", p); !errors.Is(err, ErrBadPath) {
			t.Errorf("Move(main.tex, %q) err = %v, want ErrBadPath", p, err)
		}
	}
	// A symlink planted in the tree (e.g. by a future git checkout) must not lead out.
	os.Symlink("/etc", filepath.Join(s.Path(id), "escape"))
	if _, _, err := s.ReadFile(ctx, id, "escape/hosts"); err == nil {
		t.Fatal("read through a symlink out of the project")
	}
	if _, _, err := s.ReadFile(ctx, "nope", "main.tex"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown project: %v", err)
	}
}

func TestFileOperations(t *testing.T) {
	ctx := ownerCtx
	s, id := newProject(t)

	b, text, err := s.ReadFile(ctx, id, "main.tex")
	if err != nil || !text || string(b) != doc {
		t.Fatalf("ReadFile = %q %v %v", b, text, err)
	}
	if _, text, _ := s.ReadFile(ctx, id, "fig/logo.png"); text {
		t.Fatal("binary file reported as text")
	}

	if err := s.WriteFile(ctx, id, "chapters/new.tex", strings.NewReader("New")); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile(ctx, id, "main.tex", strings.NewReader("changed")); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.ReadFile(ctx, id, "main.tex"); string(b) != "changed" {
		t.Fatalf("overwrite: %q", b)
	}
	if err := s.WriteFile(ctx, id, "sec", strings.NewReader("x")); !errors.Is(err, ErrBadPath) {
		t.Fatalf("writing over a folder: %v", err)
	}

	if err := s.Move(ctx, id, "sec/intro.tex", "main.tex"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("move onto existing file: %v", err)
	}
	if err := s.Move(ctx, id, "sec", "sec/inner"); !errors.Is(err, ErrBadPath) {
		t.Fatalf("move into itself: %v", err)
	}
	if err := s.Move(ctx, id, "sec", "parts/sections"); err != nil {
		t.Fatal(err)
	}
	if err := s.Mkdir(ctx, id, "empty/dir"); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, id, "fig"); err != nil {
		t.Fatal(err)
	}

	files, err := s.ListFiles(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range files {
		got = append(got, f.Path)
	}
	want := "chapters,chapters/new.tex,empty,empty/dir,main.tex,parts,parts/sections,parts/sections/intro.tex"
	if strings.Join(got, ",") != want {
		t.Fatalf("tree = %v\nwant   %s", got, want)
	}
	for _, f := range files {
		if strings.Contains(f.Path, "refract-tmp") {
			t.Fatalf("temp file left behind: %s", f.Path)
		}
	}
}

func TestWriteFileTooLarge(t *testing.T) {
	ctx := ownerCtx
	s, id := newProject(t)
	big := strings.NewReader(strings.Repeat("x", MaxFileSize+1))
	if err := s.WriteFile(ctx, id, "main.tex", big); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
	if b, _, _ := s.ReadFile(ctx, id, "main.tex"); string(b) != doc {
		t.Fatal("failed write clobbered the file")
	}
}

func TestCheckpointCommitsOnlyChanges(t *testing.T) {
	ctx := ownerCtx
	s, id := newProject(t)
	if made, err := s.Checkpoint(ctx, id, "checkpoint: compile"); err != nil || made {
		t.Fatalf("clean tree: made=%v err=%v", made, err)
	}
	s.WriteFile(ctx, id, "main.tex", strings.NewReader("edited"))
	os.MkdirAll(filepath.Join(s.Path(id), ".refract/build"), 0o755)
	os.WriteFile(filepath.Join(s.Path(id), ".refract/build/main.pdf"), []byte("pdf"), 0o644)
	if made, err := s.Checkpoint(ctx, id, "checkpoint: compile"); err != nil || !made {
		t.Fatalf("edited tree: made=%v err=%v", made, err)
	}
	out, _ := git(ctx, s.Path(id), "show", "--stat", "--format=%s", "HEAD")
	if !strings.HasPrefix(out, "checkpoint: compile") || !strings.Contains(out, "main.tex") || strings.Contains(out, ".refract") {
		t.Fatalf("HEAD = %q", out)
	}
}
