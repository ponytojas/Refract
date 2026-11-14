package compile

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sandboxOrSkip skips where bubblewrap can't run, unless REFRACT_TEST_SANDBOX=1
// (CI): there a missing tool is a failure, so the tests can't silently vanish.
func sandboxOrSkip(t *testing.T) {
	t.Helper()
	why := ""
	switch {
	case runtime.GOOS != "linux":
		why = "bubblewrap needs Linux"
	case exec.Command("bwrap", "--unshare-all", "--ro-bind", "/", "/", "--", "true").Run() != nil:
		why = "bwrap can't run here (not installed, or user namespaces are blocked)"
	}
	if why == "" {
		if _, err := exec.LookPath("prlimit"); err != nil {
			why = "prlimit is not installed"
		}
	}
	if why != "" {
		if os.Getenv("REFRACT_TEST_SANDBOX") == "1" {
			t.Fatalf("REFRACT_TEST_SANDBOX=1 but %s", why)
		}
		t.Skip(why)
	}
}

const marker = "refract-test-secret-marker-9f3a"

type sandboxEnv struct {
	root, data, project, other string
	tect                       *Tectonic
}

// newSandboxEnv lays out a data folder like the server's and a fake "tectonic":
// a shell script, so no TeX is needed.
func newSandboxEnv(t *testing.T, script string) *sandboxEnv {
	t.Helper()
	sandboxOrSkip(t)
	t.Setenv("REFRACT_SECRET_KEY", marker)
	root := t.TempDir()
	e := &sandboxEnv{root: root, data: filepath.Join(root, "data")}
	e.project = filepath.Join(e.data, "projects", "mine")
	e.other = filepath.Join(e.data, "projects", "theirs")
	for _, d := range []string{filepath.Join(e.project, ".git"), e.other, filepath.Join(root, "bundle"), filepath.Join(root, "cache"), filepath.Join(root, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(p, s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(e.data, "refract.db"), "database "+marker)
	write(filepath.Join(e.project, "main.tex"), "mine")
	write(filepath.Join(e.other, "main.tex"), "theirs")
	bin := filepath.Join(root, "bin", "tectonic")
	write(bin, "#!/bin/sh\n"+script)
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	e.tect = &Tectonic{
		Bin: bin, Bundle: filepath.Join(root, "bundle"), CacheDir: filepath.Join(root, "cache"),
		Timeout: 20 * time.Second, Sandbox: &Sandbox{},
	}
	return e
}

func (e *sandboxEnv) report(t *testing.T) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.project, filepath.FromSlash(BuildDir), "report.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		k, v, _ := strings.Cut(l, " ")
		got[k] = v
	}
	return got
}

// The script gets --outdir like the real one and reports what it could do.
const probeScript = `
while [ $# -gt 0 ]; do case "$1" in --outdir) out=$2;; esac; shift; done
r="$out/report.txt"; : > "$r"
try() { name=$1; shift; if "$@" >/dev/null 2>&1; then echo "$name yes" >> "$r"; else echo "$name no" >> "$r"; fi; }
try environ-1 grep -qa %[1]s /proc/1/environ
try environ-self grep -qa %[1]s /proc/self/environ
try database cat %[2]s/refract.db
ls %[2]s %[2]s/projects > "$out/listing" 2>&1
try other-project cat %[3]s/main.tex
try write-git touch %[4]s/.git/x
try write-project touch %[4]s/x
try write-etc touch /etc/x
try read-project cat %[4]s/main.tex
try write-build touch "$out/ok"
echo "ifaces $(tail -n +3 /proc/net/dev | cut -d: -f1 | tr -d ' ' | tr '\n' ,)" >> "$r"
`

func TestSandboxIsolation(t *testing.T) {
	e := newSandboxEnv(t, "")
	script := fmt.Sprintf(probeScript, marker, e.data, e.other, e.project)
	b, _ := os.ReadFile(e.tect.Bin)
	os.WriteFile(e.tect.Bin, append(b, script...), 0o755)

	res, err := e.tect.Compile(context.Background(), e.project, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if res.TimedOut || res.Superseded {
		t.Fatalf("result = %+v", res)
	}
	got := e.report(t)
	for _, name := range []string{"environ-1", "environ-self", "database", "other-project", "write-git", "write-project", "write-etc"} {
		if got[name] != "no" {
			t.Errorf("%s: %q; the sandbox should have refused it", name, got[name])
		}
	}
	for _, name := range []string{"read-project", "write-build"} {
		if got[name] != "yes" {
			t.Errorf("%s: %q; the sandbox should allow it", name, got[name])
		}
	}
	// The folders above the project exist, but hold nothing else.
	listing, _ := os.ReadFile(filepath.Join(e.project, filepath.FromSlash(BuildDir), "listing"))
	for _, leak := range []string{"refract.db", "theirs"} {
		if strings.Contains(string(listing), leak) {
			t.Errorf("the data folder shows %q:\n%s", leak, listing)
		}
	}
	if got["ifaces"] != "lo," {
		t.Errorf("network interfaces = %q; want only lo", got["ifaces"])
	}
	if _, err := os.Stat(filepath.Join(e.project, ".git", "x")); err == nil {
		t.Error("a file appeared in .git")
	}
}

// A compile of an assistant's worktree (a folder inside the project's .refract) works the same.
func TestSandboxWorktree(t *testing.T) {
	e := newSandboxEnv(t, `
while [ $# -gt 0 ]; do case "$1" in --outdir) out=$2;; esac; shift; done
cat main.tex > "$out/seen.txt"
`)
	wt := filepath.Join(e.project, ".refract", "worktrees", "turn1")
	os.MkdirAll(wt, 0o755)
	os.WriteFile(filepath.Join(wt, "main.tex"), []byte("worktree"), 0o644)
	if _, err := e.tect.Compile(context.Background(), wt, "main.tex"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(wt, filepath.FromSlash(BuildDir), "seen.txt"))
	if err != nil || string(b) != "worktree" {
		t.Fatalf("seen = %q, %v", b, err)
	}
}

func TestSandboxTimeoutKillsEverything(t *testing.T) {
	e := newSandboxEnv(t, `
while [ $# -gt 0 ]; do case "$1" in --outdir) out=$2;; esac; shift; done
( i=0; while :; do i=$((i+1)); echo $i >> "$out/tick"; done ) &
while :; do :; done
`)
	e.tect.Timeout = 2 * time.Second
	start := time.Now()
	res, err := e.tect.Compile(context.Background(), e.project, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut {
		t.Fatalf("result = %+v; want TimedOut", res)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v to stop", time.Since(start))
	}
	tick := filepath.Join(e.project, filepath.FromSlash(BuildDir), "tick")
	size := func() int64 {
		fi, err := os.Stat(tick)
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	a := size()
	time.Sleep(500 * time.Millisecond)
	if b := size(); a == 0 || a != b {
		t.Errorf("the background loop is still running (tick file of %d bytes, then %d)", a, b)
	}
}

func TestSandboxLimits(t *testing.T) {
	e := newSandboxEnv(t, `
while [ $# -gt 0 ]; do case "$1" in --outdir) out=$2;; esac; shift; done
for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
  sleep 2 &
  echo $i >> "$out/started"
done
`)
	e.tect.Sandbox.Processes = 6
	if _, err := e.tect.Compile(context.Background(), e.project, "main.tex"); err != nil {
		t.Fatal(err)
	}
	// The shell dies at the first fork the limit refuses; the file says how far it got.
	b, _ := os.ReadFile(filepath.Join(e.project, filepath.FromSlash(BuildDir), "started"))
	lines := strings.Fields(string(b))
	n := 0
	if len(lines) > 0 {
		n, _ = strconv.Atoi(lines[len(lines)-1])
	}
	if n == 0 || n >= 20 {
		t.Errorf("started %d background processes with a limit of 6", n)
	}
}

func TestSandboxCheck(t *testing.T) {
	// The fake tectonic writes a PDF, so the self-test passes.
	e := newSandboxEnv(t, `
while [ $# -gt 0 ]; do case "$1" in --outdir) out=$2;; esac; shift; done
echo pdf > "$out/main.pdf"
`)
	if err := e.tect.Sandbox.Check(context.Background(), e.tect); err != nil {
		t.Fatalf("Check = %v; want it to pass", err)
	}

	missing := *e.tect
	missing.Bundle = filepath.Join(e.root, "no-such-bundle")
	if err := e.tect.Sandbox.Check(context.Background(), &missing); err == nil || !strings.Contains(err.Error(), "REFRACT_TECTONIC_BUNDLE") {
		t.Errorf("missing bundle: Check = %v; want an error naming REFRACT_TECTONIC_BUNDLE", err)
	}
	noCache := *e.tect
	noCache.CacheDir = ""
	if err := e.tect.Sandbox.Check(context.Background(), &noCache); err == nil || !strings.Contains(err.Error(), "TECTONIC_CACHE_DIR") {
		t.Errorf("no cache: Check = %v; want an error naming TECTONIC_CACHE_DIR", err)
	}
	noBwrap := &Sandbox{Bwrap: "no-such-bwrap"}
	if err := noBwrap.Check(context.Background(), e.tect); err == nil || !strings.Contains(err.Error(), "bubblewrap") {
		t.Errorf("no bwrap: Check = %v; want an error naming bubblewrap", err)
	}

	// A tectonic that fails (a format file missing, say): its output is in the error.
	bad := newSandboxEnv(t, "echo 'error: cannot write format file texput.fmt' >&2\nexit 1\n")
	err := bad.tect.Sandbox.Check(context.Background(), bad.tect)
	if err == nil || !strings.Contains(err.Error(), "cannot write format file") {
		t.Errorf("failing tectonic: Check = %v; want its output in the error", err)
	}
}
