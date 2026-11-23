//go:build unix

package tools

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBundledGitSupportTrust(t *testing.T) {
	dir := install(t)
	git := fake(t, filepath.Join(dir, "git", "bin", "git"))
	helpers := filepath.Join(dir, "git", "libexec", "git-core")
	helper := fake(t, filepath.Join(helpers, "git-remote-http"))
	template := fake(t, filepath.Join(dir, "git", "share", "git-core", "templates", "template"))
	if got := Find("git"); got.Source != SourceBundled {
		t.Fatalf("trusted Git = %+v", got)
	}
	for _, p := range []string{helpers, helper, template} {
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, fi.Mode().Perm()|0o020); err != nil {
			t.Fatal(err)
		}
		if Find("git").Found() {
			t.Errorf("accepted writable %s", p)
		}
		if err := os.Chmod(p, fi.Mode().Perm()); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(helpers, "git-add")
	if err := os.Symlink(filepath.Join("..", "..", "bin", "git"), link); err != nil {
		t.Fatal(err)
	}
	if got := Find("git"); got.Path != git {
		t.Errorf("internal symlink rejected: %+v", got)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	external := fake(t, filepath.Join(t.TempDir(), "external"))
	external, err := filepath.EvalSymlinks(external)
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.Rel(helpers, external)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"missing", outside} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if Find("git").Found() {
			t.Errorf("accepted Git with helper symlink to %s", target)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Rename(helpers, helpers+"-absent"); err != nil {
		t.Fatal(err)
	}
	if Find("git").Found() {
		t.Error("accepted Git without helper directory")
	}
}

// A symlink as the resource folder itself is refused, even when it points at
// a good folder: the walk would not look inside it.
func TestResourceRootSymlinkRefused(t *testing.T) {
	dir := install(t)
	real := filepath.Join(t.TempDir(), "real-seed")
	if err := os.MkdirAll(filepath.Join(real, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(dir, "tex-seed")); err != nil {
		t.Fatal(err)
	}
	if got := Resource("tex-seed"); got != "" {
		t.Fatalf("symlinked root should be ignored, got %q", got)
	}
}
