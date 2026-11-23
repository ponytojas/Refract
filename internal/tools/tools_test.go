package tools

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fake(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// install lays out <root>/bin/refract and returns the bundled folder.
func install(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake shell executables")
	}
	root := t.TempDir()
	exe := fake(t, filepath.Join(root, "bin", "refract"))
	executable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { executable = os.Executable })
	t.Setenv("PATH", t.TempDir()) // empty: nothing found on the PATH
	for _, n := range Names {
		t.Setenv(EnvVar(n), "")
	}
	d, _ := filepath.EvalSymlinks(root)
	return filepath.Join(d, "libexec", "refract")
}

func TestBundledOnly(t *testing.T) {
	dir := install(t)
	if got := Missing(); len(got) != len(Names) {
		t.Fatalf("nothing installed, missing = %v", got)
	}
	fake(t, filepath.Join(dir, "tectonic"))
	fake(t, filepath.Join(dir, "synctex"))
	fake(t, filepath.Join(dir, "rg"))
	fake(t, filepath.Join(dir, "git", "bin", "git"))
	fake(t, filepath.Join(dir, "git", "libexec", "git-core", "git"))
	for _, tl := range All() {
		if !tl.Found() || tl.Source != SourceBundled || !filepath.IsAbs(tl.Path) {
			t.Errorf("%+v", tl)
		}
	}
	if got := Find("rg").Path; got != filepath.Join(dir, "rg") {
		t.Errorf("rg = %s", got)
	}
}

func TestPrecedence(t *testing.T) {
	dir := install(t)
	onPath := t.TempDir()
	t.Setenv("PATH", onPath)
	fake(t, filepath.Join(onPath, "rg"))
	if got := Find("rg"); got.Source != SourcePath || got.Path != filepath.Join(onPath, "rg") {
		t.Errorf("path: %+v", got)
	}
	fake(t, filepath.Join(dir, "rg"))
	if got := Find("rg"); got.Source != SourceBundled {
		t.Errorf("bundled beats PATH: %+v", got)
	}
	over := fake(t, filepath.Join(t.TempDir(), "my-rg"))
	t.Setenv("REFRACT_RG_BIN", over)
	if got := Find("rg"); got.Source != SourceEnv || got.Path != over {
		t.Errorf("env beats bundled: %+v", got)
	}
	nope := filepath.Join(t.TempDir(), "nope")
	t.Setenv("REFRACT_RG_BIN", nope)
	if got := Find("rg"); got.Found() {
		t.Errorf("a broken override is final: %+v", got)
	}
	if got, err := Path("rg"); err == nil || got != "" {
		t.Errorf("Path with a broken override = %q, %v; want empty path and error", got, err)
	}
	t.Setenv("REFRACT_RG_BIN", "")
	if err := os.Remove(filepath.Join(dir, "rg")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REFRACT_RG_BIN", "not-rg") // a name, looked up on the PATH: not there
	if got, err := Path("rg"); err == nil || got != "" {
		t.Errorf("Path with a missing name = %q, %v; want empty path and error", got, err)
	}
	t.Setenv("REFRACT_RG_BIN", "missing/rg")
	if got, err := Path("rg"); err == nil || got != "" {
		t.Errorf("Path with a missing relative override = %q, %v", got, err)
	}
	t.Setenv("REFRACT_RG_BIN", over)
	if got, err := Path("rg"); err != nil || got != over || !filepath.IsAbs(got) {
		t.Errorf("Path with a valid override = %q, %v", got, err)
	}
}

func TestSymlinkedExecutable(t *testing.T) {
	dir := install(t)
	fake(t, filepath.Join(dir, "synctex"))
	real, _ := executable()
	link := filepath.Join(t.TempDir(), "refract")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	executable = func() (string, error) { return link, nil }
	if got := Find("synctex"); got.Source != SourceBundled {
		t.Errorf("%+v", got)
	}
}

func TestGitEnv(t *testing.T) {
	dir := install(t)
	git := fake(t, filepath.Join(dir, "git", "bin", "git"))
	if err := os.MkdirAll(filepath.Join(dir, "git", "share", "git-core", "templates"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := strings.Join(GitEnv(git), "\n")
	for _, want := range []string{
		"GIT_EXEC_PATH=" + filepath.Join(dir, "git", "libexec", "git-core"),
		"GIT_TEMPLATE_DIR=" + filepath.Join(dir, "git", "share", "git-core", "templates"),
		"GIT_CONFIG_NOSYSTEM=1",
	} {
		if !strings.Contains(env, want) {
			t.Errorf("env %q lacks %q", env, want)
		}
	}
	if got := GitEnv("/usr/bin/git"); got != nil {
		t.Errorf("a system git gets no changes: %v", got)
	}
}

func TestUntrustedBundledFolderIgnored(t *testing.T) {
	dir := install(t)
	fake(t, filepath.Join(dir, "tectonic"))
	fake(t, filepath.Join(dir, "git", "bin", "git"))
	fake(t, filepath.Join(dir, "git", "libexec", "git-core", "git"))
	if !Find("tectonic").Found() {
		t.Fatal("a private bundled folder should be used")
	}
	for name, path := range map[string]string{
		"folder":    dir,
		"libexec":   filepath.Dir(dir),
		"git/bin":   filepath.Join(dir, "git", "bin"),
		"tool file": filepath.Join(dir, "tectonic"),
	} {
		fi, _ := os.Stat(path)
		mode := fi.Mode().Perm()
		if err := os.Chmod(path, mode|0o020); err != nil {
			t.Fatal(err)
		}
		bad := Find("tectonic").Found()
		if name == "git/bin" {
			bad = Find("git").Found()
		}
		if bad {
			t.Errorf("group-writable %s should be ignored", name)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(dir, "tectonic"), 0o757); err != nil {
		t.Fatal(err)
	}
	if Find("tectonic").Found() {
		t.Error("a tool writable by others should be ignored")
	}
	if !Find("git").Found() {
		t.Error("the other tools stay usable")
	}
}

func TestUntrustedOwner(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root owns everything it may use")
	}
	if why := unsafe("/"); why != "" && !strings.Contains(why, "writable") {
		t.Errorf("/ is owned by root: %s", why)
	}
	// A file owned by someone else, neither us nor root, is not available to
	// create here; check at least that our own files pass.
	if why := unsafe(fake(t, filepath.Join(t.TempDir(), "x"))); why != "" {
		t.Errorf("own file: %s", why)
	}
}

func TestWindowsGitIsMinGit(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	exe := fake(t, filepath.Join(root, "refract.exe"))
	executable = func() (string, error) { return exe, nil }
	goos = "windows"
	t.Cleanup(func() { executable, goos = os.Executable, runtime.GOOS })
	t.Setenv("PATH", t.TempDir())
	t.Setenv(EnvVar("git"), "")
	fake(t, filepath.Join(root, "git", "bin", "git.exe")) // not MinGit's entry point
	if got := Find("git"); got.Found() {
		t.Fatalf("git/bin/git.exe should not be used on Windows: %+v", got)
	}
	git := fake(t, filepath.Join(root, "git", "cmd", "git.exe"))
	if got := Find("git"); got.Source != SourceBundled || got.Path != git {
		t.Fatalf("%+v, want %s", got, git)
	}
	if env := GitEnv(git); len(env) != 1 || env[0] != "GIT_CONFIG_NOSYSTEM=1" {
		t.Errorf("MinGit finds its own helpers; env = %v", env)
	}
}

func TestResource(t *testing.T) {
	dir := install(t)
	if got := Resource("tex-seed"); got != "" {
		t.Fatalf("nothing installed, got %q", got)
	}
	seed := filepath.Join(dir, "tex-seed")
	if err := os.MkdirAll(filepath.Join(seed, "cache"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := Resource("tex-seed"); got != seed {
		t.Fatalf("Resource = %q, want %q", got, seed)
	}
	if runtime.GOOS == "windows" {
		return
	}
	for name, path := range map[string]string{"seed": seed, "folder": dir, "libexec": filepath.Dir(dir)} {
		fi, _ := os.Stat(path)
		mode := fi.Mode().Perm()
		if err := os.Chmod(path, mode|0o020); err != nil {
			t.Fatal(err)
		}
		if got := Resource("tex-seed"); got != "" {
			t.Errorf("group-writable %s should be ignored, got %q", name, got)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	if got := Resource("tex-seed"); got != seed {
		t.Errorf("after restoring modes: %q", got)
	}
}

// What a resource folder holds is read and trusted too: a writable file or
// folder inside a private one, or a symlink out of it, makes it untrusted.
func TestResourceChecksItsContents(t *testing.T) {
	dir := install(t)
	seed := filepath.Join(dir, "tex-seed")
	manifest := filepath.Join(seed, "seed.json")
	inner := filepath.Join(seed, "cache", "bundles")
	file := filepath.Join(inner, "a.sty")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{manifest, file} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := Resource("tex-seed"); got != seed {
		t.Fatalf("private seed: Resource = %q", got)
	}
	for name, path := range map[string]string{"manifest": manifest, "inner folder": inner, "inner file": file} {
		fi, _ := os.Stat(path)
		mode := fi.Mode().Perm()
		if err := os.Chmod(path, mode|0o002); err != nil {
			t.Fatal(err)
		}
		if got := Resource("tex-seed"); got != "" {
			t.Errorf("%s writable by others should make the seed untrusted, got %q", name, got)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "evil.sty")
	if err := os.WriteFile(outside, []byte("x"), 0o666); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(inner, "b.sty")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if got := Resource("tex-seed"); got != "" {
		t.Errorf("a symlink inside the seed should make it untrusted, got %q", got)
	}
	os.Remove(link)
	if got := Resource("tex-seed"); got != seed {
		t.Errorf("after cleaning up: %q", got)
	}
}
