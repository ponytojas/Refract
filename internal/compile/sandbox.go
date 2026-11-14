package compile

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	CPUSeconds   int   // the compile timeout plus a margin (60 s without one)
	FileSize     int64 // 200 MiB, the largest file a process may write
	Processes    int   // 64, threads included
	AddressSpace int64 // 1 GiB

	once         sync.Once
	prepErr      error
	bwrapPath    string
	prlimitPath  string
	tectonicPath string
	base         []string // the fixed part of the bwrap arguments
}

const (
	defaultCPU       = 60
	cpuMargin        = 5
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
	Timeout  time.Duration // the compile's wall-clock limit; sets the CPU limit
}

// prepare resolves, once, the programs and the root-level symlinks the
// sandbox is built from, and refuses a setuid bwrap (see "Compile sandbox").
func (s *Sandbox) prepare(tectonic string) error {
	s.once.Do(func() { s.prepErr = s.resolve(tectonic) })
	return s.prepErr
}

func (s *Sandbox) resolve(tectonic string) error {
	var err error
	if s.bwrapPath, err = exec.LookPath(cmp.Or(s.Bwrap, "bwrap")); err != nil {
		return err
	}
	if s.prlimitPath, err = exec.LookPath(cmp.Or(s.Prlimit, "prlimit")); err != nil {
		return err
	}
	if fi, err := os.Stat(s.bwrapPath); err == nil && fi.Mode()&os.ModeSetuid != 0 {
		return fmt.Errorf("%s is setuid: the process limit relies on user namespaces and a bwrap that isn't setuid (%s)", s.bwrapPath, sandboxHelp)
	}
	bin, err := exec.LookPath(tectonic)
	if err != nil {
		return err
	}
	if bin, err = filepath.Abs(bin); err != nil {
		return err
	}
	if s.tectonicPath, err = filepath.EvalSymlinks(bin); err != nil {
		return err
	}
	s.base = []string{"--unshare-all", "--die-with-parent", "--new-session", "--clearenv", "--ro-bind", "/usr", "/usr"}
	for _, p := range []string{"/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32"} {
		fi, err := os.Lstat(p)
		switch {
		case err != nil:
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			s.base = append(s.base, "--symlink", target, p)
		case fi.IsDir():
			s.base = append(s.base, "--ro-bind", p, p)
		}
	}
	// Fontconfig's configuration and the loader's cache: nothing else of /etc.
	for _, p := range []string{"/etc/fonts", "/etc/ld.so.cache"} {
		if _, err := os.Stat(p); err == nil {
			s.base = append(s.base, "--ro-bind", p, p)
		}
	}
	return nil
}

// command builds the bwrap command line for one compile.
func (s *Sandbox) command(ctx context.Context, j sandboxJob) (*exec.Cmd, error) {
	if err := s.prepare(j.Tectonic); err != nil {
		return nil, err
	}
	if !filepath.IsAbs(j.Bundle) || !filepath.IsAbs(j.Cache) {
		return nil, fmt.Errorf("the sandbox needs REFRACT_TECTONIC_BUNDLE and TECTONIC_CACHE_DIR to be absolute paths (%s)", sandboxHelp)
	}
	cpu := s.CPUSeconds
	if cpu == 0 {
		cpu = defaultCPU
		if j.Timeout > 0 { // a little over the wall-clock limit, so that one normally comes first
			cpu = int((j.Timeout+time.Second-1)/time.Second) + cpuMargin
		}
	}

	args := slices.Clone(s.base)
	// --tmpfs /tmp comes first: a project under /tmp is mounted on top of it.
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--ro-bind", s.tectonicPath, s.tectonicPath,
		"--ro-bind", j.Bundle, j.Bundle,
		"--ro-bind", j.Cache, j.Cache,
		"--ro-bind", j.Project, j.Project,
		"--bind", j.OutDir, j.OutDir,
		"--setenv", "PATH", "/usr/bin:/bin", "--setenv", "HOME", "/tmp", "--setenv", "TECTONIC_CACHE_DIR", j.Cache,
		"--chdir", j.WorkDir,
		// The limits are applied inside: RLIMIT_NPROC counts every thread of the
		// user, so around bwrap a busy server would fail to start it.
		"--", s.prlimitPath,
		"--cpu="+strconv.Itoa(cpu),
		"--fsize="+strconv.FormatInt(cmp.Or(s.FileSize, defaultFileSize), 10),
		"--nproc="+strconv.Itoa(cmp.Or(s.Processes, defaultProcesses)),
		"--as="+strconv.FormatInt(cmp.Or(s.AddressSpace, defaultAddress), 10),
		"--", s.tectonicPath)
	args = append(args, j.Args...)

	cmd := exec.CommandContext(ctx, s.bwrapPath, args...)
	cmd.Env = []string{} // bwrap sets the sandbox's own
	return cmd, nil
}

// Check verifies, at startup, that compiles will work inside the sandbox:
// the tools exist, the bundle and cache are there, and a one-line document
// compiles. The last step catches a host that blocks user namespaces,
// bundle files that aren't readable, and a missing or mismatched format file.
func (s *Sandbox) Check(ctx context.Context, t *Tectonic) error {
	for _, tool := range []struct{ name, env string }{{cmp.Or(s.Bwrap, "bwrap"), "bubblewrap"}, {cmp.Or(s.Prlimit, "prlimit"), "util-linux"}} {
		if _, err := exec.LookPath(tool.name); err != nil {
			return fmt.Errorf("REFRACT_SANDBOX=bwrap needs %s on the PATH: install the %s package (%s)", tool.name, tool.env, sandboxHelp)
		}
	}
	if _, err := exec.LookPath(cmp.Or(t.Bin, "tectonic")); err != nil {
		return fmt.Errorf("REFRACT_SANDBOX=bwrap needs tectonic on the PATH (%s)", sandboxHelp)
	}
	if err := s.prepare(cmp.Or(t.Bin, "tectonic")); err != nil {
		return fmt.Errorf("REFRACT_SANDBOX=bwrap: %w", err)
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
