package compile

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Sandbox runs Tectonic inside bubblewrap (Linux): no network, a read-only
// view of the system, the TeX bundle and the one project being built, and
// nothing else of the host (not the database, other projects, or this
// process's environment). See "Compile sandbox" in the README.
type Sandbox struct {
	Bwrap   string // "bwrap" if empty
	Prlimit string // "prlimit" if empty

	// Limits applied to Tectonic and everything it starts; zero = the default.
	CPUSeconds   int   // 60
	FileSize     int64 // 200 MiB, the largest file a process may write
	Processes    int   // 64, threads included
	AddressSpace int64 // 1 GiB
}

const (
	defaultCPU       = 60
	defaultFileSize  = 200 << 20
	defaultProcesses = 64
	defaultAddress   = 1 << 30
)

const sandboxHelp = "see \"Compile sandbox\" in the README"

type sandboxJob struct {
	Tectonic string // the executable, as the server finds it
	Args     []string
	Bundle   string // local folder
	Cache    string
	Project  string // the project folder, or an assistant turn's worktree: read-only
	OutDir   string // inside Project: the only writable place
	WorkDir  string
}

// command builds the bwrap command line for one compile.
func (s *Sandbox) command(ctx context.Context, j sandboxJob) (*exec.Cmd, error) {
	bin, err := exec.LookPath(j.Tectonic)
	if err != nil {
		return nil, err
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return nil, err
	}
	if bin, err = filepath.EvalSymlinks(bin); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(j.Bundle) || !filepath.IsAbs(j.Cache) {
		return nil, fmt.Errorf("the sandbox needs REFRACT_TECTONIC_BUNDLE and TECTONIC_CACHE_DIR to be absolute paths (%s)", sandboxHelp)
	}

	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--clearenv", "--ro-bind", "/usr", "/usr"}
	for _, p := range []string{"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"} {
		fi, err := os.Lstat(p)
		switch {
		case err != nil:
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return nil, err
			}
			args = append(args, "--symlink", target, p)
		case fi.IsDir():
			args = append(args, "--ro-bind", p, p)
		}
	}
	// --tmpfs /tmp comes first: a project under /tmp is mounted on top of it.
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--ro-bind", bin, bin,
		"--ro-bind", j.Bundle, j.Bundle,
		"--ro-bind", j.Cache, j.Cache,
		"--ro-bind", j.Project, j.Project,
		"--bind", j.OutDir, j.OutDir,
		"--setenv", "PATH", "/usr/bin:/bin", "--setenv", "HOME", "/tmp", "--setenv", "TECTONIC_CACHE_DIR", j.Cache,
		"--chdir", j.WorkDir,
		// The limits are applied inside: RLIMIT_NPROC counts every thread of the
		// user, so around bwrap a busy server would fail to start it.
		"--", cmpOr(s.Prlimit, "prlimit"),
		"--cpu="+strconv.Itoa(orInt(s.CPUSeconds, defaultCPU)),
		"--fsize="+strconv.FormatInt(orInt64(s.FileSize, defaultFileSize), 10),
		"--nproc="+strconv.Itoa(orInt(s.Processes, defaultProcesses)),
		"--as="+strconv.FormatInt(orInt64(s.AddressSpace, defaultAddress), 10),
		"--", bin)
	args = append(args, j.Args...)

	bwrap, err := exec.LookPath(cmpOr(s.Bwrap, "bwrap"))
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Env = []string{} // bwrap sets the sandbox's own
	return cmd, nil
}

func orInt(v, def int) int {
	if v != 0 {
		return v
	}
	return def
}

func orInt64(v, def int64) int64 {
	if v != 0 {
		return v
	}
	return def
}

// Check verifies, at startup, that compiles will work inside the sandbox:
// the tools exist, the bundle and cache are there, and a one-line document
// compiles. The last step catches a host that blocks user namespaces,
// bundle files that aren't readable, and a missing or mismatched format file.
func (s *Sandbox) Check(ctx context.Context, t *Tectonic) error {
	for _, tool := range []struct{ name, env string }{{cmpOr(s.Bwrap, "bwrap"), "bubblewrap"}, {cmpOr(s.Prlimit, "prlimit"), "util-linux"}} {
		if _, err := exec.LookPath(tool.name); err != nil {
			return fmt.Errorf("REFRACT_SANDBOX=bwrap needs %s on the PATH: install the %s package (%s)", tool.name, tool.env, sandboxHelp)
		}
	}
	if _, err := exec.LookPath(cmpOr(t.Bin, "tectonic")); err != nil {
		return fmt.Errorf("REFRACT_SANDBOX=bwrap needs tectonic on the PATH (%s)", sandboxHelp)
	}
	if fi, err := os.Stat(t.Bundle); t.Bundle == "" || err != nil || !fi.IsDir() {
		return fmt.Errorf("REFRACT_SANDBOX=bwrap needs REFRACT_TECTONIC_BUNDLE=%q to be a local folder with the TeX bundle, because the sandbox has no network (%s)", t.Bundle, sandboxHelp)
	}
	if fi, err := os.Stat(t.CacheDir); t.CacheDir == "" || err != nil || !fi.IsDir() {
		return fmt.Errorf("REFRACT_SANDBOX=bwrap needs TECTONIC_CACHE_DIR=%q to be an existing folder holding the format file (%s)", t.CacheDir, sandboxHelp)
	}

	dir, err := os.MkdirTemp("", "refract-sandbox-check-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	if err := os.WriteFile(filepath.Join(dir, "main.tex"), []byte("\\documentclass{article}\\begin{document}x\\end{document}\n"), 0o644); err != nil {
		return err
	}
	probe := *t
	probe.Fonts = nil
	probe.Sandbox = s
	probe.Timeout = min(max(t.Timeout, 30*time.Second), 2*time.Minute)
	res, err := probe.Compile(ctx, dir, "main.tex")
	if err == nil && res.OK && res.PDF != "" {
		return nil
	}
	msg := "the self-test document did not compile"
	if err != nil {
		msg = err.Error()
	} else if res.TimedOut {
		msg = "the self-test document timed out"
	}
	var tail string
	if res != nil {
		lines := strings.Split(strings.TrimSpace(res.Output), "\n")
		tail = strings.Join(lines[max(0, len(lines)-8):], "\n")
	}
	return errors.New("REFRACT_SANDBOX=bwrap: " + msg + " inside the sandbox (" + sandboxHelp + "):\n" + tail)
}
