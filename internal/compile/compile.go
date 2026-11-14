// Package compile runs LaTeX builds. Tectonic is the only engine; callers use
// the Compiler interface so a TeX Live backend could be added later.
package compile

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/fonts"
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
}

// Compiler builds mainFile (project-relative) inside projectDir.
type Compiler interface {
	Compile(ctx context.Context, projectDir, mainFile string) (*Result, error)
}

// Tectonic runs the tectonic CLI.
type Tectonic struct {
	Bin      string // "tectonic" if empty
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
	if err := prepareOutDir(projectDir); err != nil { // tectonic requires it to exist
		return nil, err
	}
	base := path.Base(mainFile)
	stem := strings.TrimSuffix(base, path.Ext(base))
	runCtx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
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
	os.Remove(filepath.Join(outDir, stem+".log"))
	// continue-on-errors: like Prism and Overleaf, produce a PDF despite errors;
	// the errors are still reported and the build does not count as OK.
	args := []string{base, "--outdir", outDir, "--synctex", "--keep-logs", "--untrusted", "-Z", "continue-on-errors"}
	if t.Bundle != "" {
		args = append(args, "--bundle", t.Bundle)
	}

	workDir := filepath.Join(sourceDir, filepath.FromSlash(path.Dir(mainFile)))
	var cmd *exec.Cmd
	if t.Sandbox != nil {
		cmd, err = t.Sandbox.command(runCtx, sandboxJob{
			Tectonic: cmp.Or(t.Bin, "tectonic"), Args: args, Bundle: t.Bundle, Cache: t.CacheDir,
			Project: projectDir, OutDir: outDir, WorkDir: workDir, Timeout: t.Timeout,
		})
		if err != nil {
			return nil, err
		}
	} else {
		cmd = exec.CommandContext(runCtx, cmp.Or(t.Bin, "tectonic"), args...)
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
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	if setup != "" {
		if err := restoreSyncPaths(filepath.Join(outDir, stem+".synctex.gz"), sourceDir, projectDir); err != nil {
			return nil, err
		}
	}
	res := &Result{DurationMS: time.Since(start).Milliseconds(), Output: stderr.String()}
	switch {
	case ctx.Err() != nil:
		res.Superseded = true
	case runCtx.Err() != nil:
		res.TimedOut = true
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) && t.Sandbox != nil && cpuLimited(exitErr) && !res.Superseded && !res.TimedOut {
		res.TimedOut, res.CPULimit = true, true
	}
	if runErr != nil && !errors.As(runErr, &exitErr) && !res.TimedOut && !res.Superseded {
		return nil, runErr // tectonic missing, not a document problem
	}

	res.Output = dropFontconfigNoise(res.Output)
	res.Problems = parseStderr(res.Output)
	if log, err := os.ReadFile(filepath.Join(outDir, stem+".log")); err == nil {
		res.Problems = append(res.Problems, parseLog(string(log))...)
	}
	locate(res.Problems, projectDir, mainFile)

	res.OK = runErr == nil && !hasError(res.Problems)
	if fi, err := os.Stat(filepath.Join(outDir, stem+".pdf")); err == nil && !fi.ModTime().Before(start.Truncate(time.Second)) && // coarse-mtime filesystems
		!res.TimedOut && !res.Superseded {
		res.PDF = path.Join(BuildDir, stem+".pdf") // produced by this run, possibly with errors
	}
	if !res.OK && !hasError(res.Problems) {
		msg := "Compilation failed."
		if res.CPULimit {
			msg = "Compilation used too much CPU time and was stopped. Look for an infinite loop."
		} else if res.TimedOut {
			msg = "Compilation timed out after " + t.Timeout.String() + " and was stopped. Look for an infinite loop."
		} else if res.Superseded {
			msg = "Cancelled by a newer compile."
		}
		res.Problems = append([]Problem{{Severity: "error", Kind: "error", Message: msg}}, res.Problems...)
	}
	return res, nil
}

// prepareOutDir creates .refract/build without following symlinks and refuses
// a project where any part of it is one: a pulled repository could otherwise
// point it at a folder outside the project, which the sandbox mounts writable.
func prepareOutDir(projectDir string) error {
	cur := projectDir
	for _, part := range strings.Split(BuildDir, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
		case err != nil:
			return err
		case fi.Mode()&os.ModeSymlink != 0:
			return fmt.Errorf("refusing to compile: %s is a symbolic link; delete it and compile again", filepath.ToSlash(strings.TrimPrefix(cur, projectDir+string(filepath.Separator))))
		case !fi.IsDir():
			return fmt.Errorf("refusing to compile: %s is not a folder", filepath.ToSlash(strings.TrimPrefix(cur, projectDir+string(filepath.Separator))))
		}
	}
	real, err := filepath.EvalSymlinks(filepath.Join(projectDir, filepath.FromSlash(BuildDir)))
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(projectDir)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(root, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("refusing to compile: %s resolves outside the project", BuildDir)
	}
	return nil
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
	return r.Compiler.Compile(ctx, projectDir, mainFile)
}
