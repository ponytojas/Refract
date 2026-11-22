// Package compile runs LaTeX builds. Tectonic is the only engine; callers use
// the Compiler interface so a TeX Live backend could be added later.
package compile

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/fonts"
	"github.com/ponytojas/Refract/internal/tools"
)

// BuildDir is where output goes, inside the project (git-excluded).
const BuildDir = ".refract/build"

// Result is the outcome of one compile.
type Result struct {
	OK         bool      `json:"ok"` // no errors (a PDF may exist even when false)
	Problems   []Problem `json:"problems"`
	Output     string    `json:"output"` // Tectonic's stderr summary
	TimedOut   bool      `json:"timed_out"`
	CPULimit   bool      `json:"-"`          // TimedOut because the sandbox's CPU limit stopped it
	Superseded bool      `json:"superseded"` // cancelled: a newer compile of the same project, or the caller went away
	DurationMS int64     `json:"duration_ms"`
	PDF        string    `json:"pdf,omitempty"` // project-relative path of the PDF, when produced
	Downloads  Downloads `json:"downloads"`
}

// Compiler builds mainFile (project-relative) inside projectDir.
type Compiler interface {
	Compile(ctx context.Context, projectDir, mainFile string) (*Result, error)
}

// Tectonic runs the tectonic CLI.
type Tectonic struct {
	Bin      string // found by internal/tools if empty
	Bundle   string // pinned bundle URL; empty = Tectonic's default
	CacheDir string // TECTONIC_CACHE_DIR; empty = Tectonic's default
	Timeout  time.Duration
	Fonts    *fonts.Resolver // optional automatic family resolution; shared across builds
	Sandbox  *Sandbox        // run tectonic inside it; nil = run it directly
}

const maxOutput = 1 << 20

func (t *Tectonic) Compile(ctx context.Context, projectDir, mainFile string) (*Result, error) {
	// tectonic runs in the main file's folder: a relative --outdir would point elsewhere.
	projectDir, err := filepath.Abs(projectDir)
	if err != nil {
		return nil, err
	}
	outDir := filepath.Join(projectDir, filepath.FromSlash(BuildDir))
	out, err := openOutDir(projectDir) // tectonic requires it to exist
	if err != nil {
		return nil, err
	}
	defer out.Close()
	if testHookOutDirOpened != nil {
		testHookOutDirOpened(projectDir)
	}
	base := path.Base(mainFile)
	stem := strings.TrimSuffix(base, path.Ext(base))
	runCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	// Cancelling procCtx kills tectonic without counting as a timeout.
	procCtx, stopRun := context.WithCancel(runCtx)
	defer stopRun()
	setup := ""
	sourceDir := projectDir
	if t.Fonts != nil {
		setup, err = t.Fonts.Prepare(runCtx, projectDir, mainFile)
		if err != nil {
			if ctx.Err() != nil {
				return &Result{Superseded: true}, nil
			}
			if runCtx.Err() != nil {
				return &Result{TimedOut: true, Problems: []Problem{{Severity: "error", Kind: "error", Message: "Font preparation timed out."}}}, nil
			}
			return nil, err
		}
		if setup != "" {
			sourceDir, err = fontSource(projectDir, mainFile, setup)
			if err != nil {
				return nil, err
			}
			defer os.RemoveAll(sourceDir)
		}
	}
	// A log left by the previous build would report stale problems if this one dies early.
	removeIn(out, stem+".log")
	// continue-on-errors: like Prism and Overleaf, produce a PDF despite errors;
	// the errors are still reported and the build does not count as OK.
	args := []string{base, "--outdir", outDir, "--synctex", "--keep-logs", "--untrusted", "-Z", "continue-on-errors"}
	if t.Bundle != "" {
		args = append(args, "--bundle", t.Bundle)
	}

	workDir := filepath.Join(sourceDir, filepath.FromSlash(path.Dir(mainFile)))
	var cmd *exec.Cmd
	if t.Sandbox != nil {
		cmd, err = t.Sandbox.command(procCtx, sandboxJob{
			Tectonic: t.bin(), Args: args, Bundle: t.Bundle, Cache: t.CacheDir,
			Project: projectDir, OutDir: outDir, Out: sandboxDir(out), WorkDir: workDir, Timeout: t.Timeout,
		})
		if err != nil {
			return nil, err
		}
	} else {
		cmd = exec.CommandContext(procCtx, t.bin(), args...)
		cmd.Dir = workDir
		// A minimal environment: the document must not be able to \input
		// /proc/self/environ and read secrets like REFRACT_SECRET_KEY.
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
		if t.CacheDir != "" {
			cmd.Env = append(cmd.Env, "TECTONIC_CACHE_DIR="+t.CacheDir)
		}
	}
	ownProcessGroup(cmd) // a timeout kills everything tectonic started
	cmd.WaitDelay = 2 * time.Second
	var stderr limitedBuffer
	var dl downloadState
	var dlMu sync.Mutex
	report := reporterFrom(ctx)
	onEvent := func(e Event) {
		dlMu.Lock()
		dl.add(e, time.Now())
		dlMu.Unlock()
		if report != nil {
			report(e)
		}
		if e.stops() {
			stopRun() // before xdvipdfmx: the previous good PDF stays
		}
	}
	// Tectonic writes its notes ("downloading …") to stdout and its warnings
	// ("failure fetching …") to stderr; each stream gets its own splitter,
	// since exec copies them from separate goroutines.
	pw, po := &progressWriter{report: onEvent}, &progressWriter{report: onEvent}
	cmd.Stderr = io.MultiWriter(&stderr, pw)
	cmd.Stdout = po

	start := time.Now()
	runErr := cmd.Run()
	po.close()
	pw.close()
	dlMu.Lock()
	downloads := dl
	dlMu.Unlock()
	if setup != "" {
		if err := restoreSyncPaths(out, stem+".synctex.gz", sourceDir, projectDir); err != nil {
			return nil, err
		}
	}
	res := &Result{DurationMS: time.Since(start).Milliseconds(), Output: stderr.String()}
	res.Downloads = Downloads{Missing: []string{}, Offline: downloads.offline}
	for f := range downloads.fetched {
		if !slices.Contains(downloads.missing, f) {
			res.Downloads.Fetched++
		}
	}
	if downloads.missing != nil {
		res.Downloads.Missing = downloads.missing
	}
	switch {
	case ctx.Err() != nil:
		res.Superseded = true
	case runCtx.Err() != nil:
		res.TimedOut = true
	}
	var exitErr *exec.ExitError
	// An early stop for a failed download kills with SIGKILL too: not the CPU limit.
	if errors.As(runErr, &exitErr) && t.Sandbox != nil && cpuLimited(exitErr) && !res.Superseded && !res.TimedOut && !downloads.offline {
		res.TimedOut, res.CPULimit = true, true
	}
	if runErr != nil && !errors.As(runErr, &exitErr) && !res.TimedOut && !res.Superseded {
		return nil, runErr // tectonic missing, not a document problem
	}

	res.Output = dropFontconfigNoise(res.Output)
	res.Problems = parseStderr(res.Output)
	if log, err := readIn(out, stem+".log"); err == nil {
		res.Problems = append(res.Problems, parseLog(string(log))...)
	}
	if res.Downloads.Offline {
		res.Problems = dropDownloadNoise(res.Problems)
		res.Problems = append([]Problem{{Severity: "error", Kind: "download", Message: downloadMessage(res.Downloads.Missing)}}, res.Problems...)
	}
	locate(res.Problems, projectDir, mainFile)

	res.OK = runErr == nil && !hasError(res.Problems)
	if fi, err := statIn(out, stem+".pdf"); err == nil && !fi.ModTime().Before(start.Truncate(time.Second)) && // coarse-mtime filesystems
		!res.TimedOut && !res.Superseded && !res.Downloads.Offline {
		res.PDF = path.Join(BuildDir, stem+".pdf") // produced by this run, possibly with errors
	}
	if !res.OK && !hasError(res.Problems) {
		msg := "Compilation failed."
		if res.CPULimit {
			msg = "Compilation used too much CPU time and was stopped. Look for an infinite loop."
		} else if res.TimedOut && downloads.downloadingAt(time.Now()) {
			res.Downloads.Interrupted = true
			msg = "Still downloading LaTeX packages when the time ran out. Compile again to continue; what was downloaded is kept."
			res.Problems = append([]Problem{{Severity: "error", Kind: "download", Message: msg}}, res.Problems...)
			return res, nil
		} else if res.TimedOut {
			msg = "Compilation timed out after " + t.Timeout.String() + " and was stopped. Look for an infinite loop."
		} else if res.Superseded {
			msg = "Cancelled by a newer compile."
		}
		res.Problems = append([]Problem{{Severity: "error", Kind: "error", Message: msg}}, res.Problems...)
	}
	return res, nil
}

// downloadMessage explains a stop caused by the package server being out of reach.
func downloadMessage(missing []string) string {
	msg := "This document needs LaTeX files that aren't on this computer yet, and the package server can't be reached. This is a network problem, not an error in your document."
	if len(missing) > 0 {
		msg += " Missing: " + strings.Join(missing, ", ") + "."
	}
	return msg
}

// ErrUnsafeBuildDir is returned when the project's build folder is a symbolic
// link or not a folder: the project has to be fixed before it can compile.
var ErrUnsafeBuildDir = errors.New("refusing to compile")

// OpenOutput opens name, a file the last compile wrote directly in the
// project's build folder, for reading, with the same checks the compiler
// applies: no part of the folder may be a symbolic link, and the file must be
// a regular file, not a link. Nothing is created. When the folder or the file
// doesn't exist, the error matches os.ErrNotExist.
func OpenOutput(projectDir, name string) (*os.File, error) {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return nil, &os.PathError{Op: "open", Path: name, Err: os.ErrInvalid}
	}
	dir, err := openBuildDir(projectDir, false)
	if err != nil {
		return nil, err
	}
	defer dir.Close()
	return openIn(dir, name)
}

// testHookOutDirOpened, when set, runs once the build folder is open: tests
// use it to swap the folder for a symlink, as a concurrent git pull could.
var testHookOutDirOpened func(projectDir string)

// readIn reads name, a file directly in dir; a symlink is refused.
func readIn(dir *dirHandle, name string) ([]byte, error) {
	f, err := openIn(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// statIn describes name, a file directly in dir; a symlink is refused.
func statIn(dir *dirHandle, name string) (os.FileInfo, error) {
	f, err := openIn(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

// dropFontconfigNoise removes the warning Tectonic prints when the system has
// no fontconfig configuration: it is harmless and means nothing to the user.
func dropFontconfigNoise(out string) string {
	if !strings.Contains(out, "Fontconfig error: Cannot load default config file") {
		return out
	}
	lines := strings.Split(out, "\n")
	kept := lines[:0]
	for _, l := range lines {
		if !strings.Contains(l, "Fontconfig error: Cannot load default config file") {
			kept = append(kept, l)
		}
	}
	return strings.Join(kept, "\n")
}

func hasError(ps []Problem) bool {
	for _, p := range ps {
		if p.Severity == "error" {
			return true
		}
	}
	return false
}

// limitedBuffer keeps the first maxOutput bytes and discards the rest.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.Len(); room > 0 {
		b.Buffer.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// Runner allows one compile per project: a new request cancels the running one
// and waits for it to stop before starting, so two builds never share an outdir.
type Runner struct {
	Compiler Compiler

	mu      sync.Mutex
	running map[string]*slot
	act     map[string]*liveActivity
}

type slot struct {
	busy   sync.Mutex // held while a compile of this project runs
	cancel context.CancelFunc
}

func (r *Runner) Compile(ctx context.Context, key, projectDir, mainFile string) (*Result, error) {
	r.mu.Lock()
	if r.running == nil {
		r.running = map[string]*slot{}
	}
	s := r.running[key]
	if s == nil {
		s = &slot{}
		r.running[key] = s
	}
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s.cancel = cancel
	r.mu.Unlock()

	s.busy.Lock()
	defer s.busy.Unlock()
	if ctx.Err() != nil { // superseded while waiting for the previous build
		return &Result{Superseded: true, Problems: []Problem{}}, nil
	}
	act := r.startActivity(key)
	defer r.endActivity(key, act)
	ctx = WithReporter(ctx, func(e Event) { r.onEvent(act, e) })
	return r.Compiler.Compile(ctx, projectDir, mainFile)
}

// bin is the tectonic to run: Bin, else the one internal/tools finds.
func (t *Tectonic) bin() string { return cmp.Or(t.Bin, tools.Path("tectonic")) }
