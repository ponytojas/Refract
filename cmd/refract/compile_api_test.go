package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/compile"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

type busyCompiler struct{}

func (busyCompiler) Compile(context.Context, string, string) (*compile.Result, error) {
	return nil, compile.ErrBusy
}

func TestCompileBusyIs429(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	mux := http.NewServeMux()
	(&compileAPI{svc: svc, runner: &compile.Runner{Compiler: busyCompiler{}}}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})

	ctx := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	p, err := svc.Create(ctx, "Paper")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteFile(ctx, p.ID, "main.tex", strings.NewReader(`\documentclass{article}`)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetMainFile(ctx, p.ID, "main.tex"); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/projects/"+p.ID+"/compile", nil).WithContext(ctx))
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var body struct{ Error struct{ Kind string } }
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Error.Kind != "compile_busy" {
		t.Fatalf("body %s (%v); want kind compile_busy", w.Body, err)
	}
}

type resultCompiler struct{ res compile.Result }

func (c resultCompiler) Compile(context.Context, string, string) (*compile.Result, error) {
	r := c.res
	return &r, nil
}

// A build that produced a PDF checkpoints even with TeX errors (Prism
// documents often keep a recoverable error); one without a PDF doesn't.
func TestCompileCheckpointsPDFWithErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		res  compile.Result
		want bool
	}{
		{"clean", compile.Result{OK: true, PDF: ".refract/build/main.pdf"}, true},
		{"pdf with errors", compile.Result{OK: false, PDF: ".refract/build/main.pdf"}, true},
		{"no pdf", compile.Result{OK: false}, false},
		{"clean without pdf", compile.Result{OK: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			db, err := store.Open(filepath.Join(dir, "t.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
			mux := http.NewServeMux()
			(&compileAPI{svc: svc, runner: &compile.Runner{Compiler: resultCompiler{tc.res}}}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})

			ctx := auth.WithUser(context.Background(), auth.User{ID: "ana"})
			p, err := svc.Create(ctx, "Paper")
			if err != nil {
				t.Fatal(err)
			}
			if err := svc.WriteFile(ctx, p.ID, "main.tex", strings.NewReader("\\documentclass{article}\n####\n")); err != nil {
				t.Fatal(err)
			}
			if err := svc.SetMainFile(ctx, p.ID, "main.tex"); err != nil {
				t.Fatal(err)
			}
			before, err := svc.History(ctx, p.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/projects/"+p.ID+"/compile", nil).WithContext(ctx))
			if w.Code != http.StatusOK {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			var body struct{ Checkpoint bool }
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			after, err := svc.History(ctx, p.ID, 100)
			if err != nil {
				t.Fatal(err)
			}
			if body.Checkpoint != tc.want || (len(after) > len(before)) != tc.want {
				t.Fatalf("checkpoint %v, commits %d -> %d; want checkpoint %v", body.Checkpoint, len(before), len(after), tc.want)
			}
		})
	}
}

// Downloads of the PDF and the log serve only regular files inside the
// project's build folder, and keep ranges and headers.
func TestOutputDownloadsAreConfined(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	mux := http.NewServeMux()
	(&compileAPI{svc: svc}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})
	ctx := auth.WithUser(context.Background(), auth.User{ID: "ana"})

	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	write := func(t *testing.T, p, content string) { t.Helper(); must(t, os.WriteFile(p, []byte(content), 0o644)) }
	const secret = "SECRET outside the project"
	outside := t.TempDir()
	write(t, filepath.Join(outside, "main.pdf"), secret)
	write(t, filepath.Join(outside, "main.log"), secret)
	must(t, os.Mkdir(filepath.Join(outside, "build"), 0o755))
	write(t, filepath.Join(outside, "build", "main.pdf"), secret)
	write(t, filepath.Join(outside, "build", "main.log"), secret)

	// project makes a project with main.tex as its main file, built: it
	// returns its id and its build folder holding main.pdf and main.log.
	project := func(t *testing.T) (string, string) {
		t.Helper()
		p, err := svc.Create(ctx, "Paper")
		must(t, err)
		must(t, svc.WriteFile(ctx, p.ID, "main.tex", strings.NewReader(`\documentclass{article}`)))
		must(t, svc.SetMainFile(ctx, p.ID, "main.tex"))
		build := filepath.Join(svc.Path(p.ID), filepath.FromSlash(compile.BuildDir))
		must(t, os.MkdirAll(build, 0o755))
		write(t, filepath.Join(build, "main.pdf"), "%PDF-1.7 built")
		write(t, filepath.Join(build, "main.log"), "This is a log")
		return p.ID, build
	}
	get := func(id, route, rng string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/projects/"+id+"/"+route, nil).WithContext(ctx)
		if rng != "" {
			r.Header.Set("Range", rng)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	link := func(t *testing.T, target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("can't make symbolic links here: %v", err)
		}
	}
	const notBuilt = `{"error":{"kind":"other","message":"not built yet"}}` + "\n"
	refused := func(t *testing.T, id string) {
		t.Helper()
		for _, route := range []string{"pdf", "log"} {
			if w := get(id, route, ""); w.Code != http.StatusNotFound || w.Body.String() != notBuilt {
				t.Errorf("%s: %d %q; want 404 %q", route, w.Code, w.Body, notBuilt)
			}
		}
	}

	t.Run("normal files, ranges and headers", func(t *testing.T) {
		id, _ := project(t)
		w := get(id, "pdf", "")
		if w.Code != http.StatusOK || w.Body.String() != "%PDF-1.7 built" {
			t.Fatalf("pdf: %d %q", w.Code, w.Body)
		}
		for k, v := range map[string]string{"Content-Type": "application/pdf", "Content-Security-Policy": "sandbox", "Cache-Control": "no-store", "X-Content-Type-Options": "nosniff", "Accept-Ranges": "bytes"} {
			if got := w.Header().Get(k); got != v {
				t.Errorf("pdf %s = %q, want %q", k, got, v)
			}
		}
		w = get(id, "pdf", "bytes=0-3")
		if w.Code != http.StatusPartialContent || w.Body.String() != "%PDF" || w.Header().Get("Content-Range") != "bytes 0-3/14" {
			t.Errorf("range: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Range"))
		}
		w = get(id, "log", "")
		if w.Code != http.StatusOK || w.Body.String() != "This is a log" || w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("log: %d %q %q", w.Code, w.Body, w.Header().Get("Content-Type"))
		}
	})

	t.Run("missing output", func(t *testing.T) {
		id, build := project(t)
		must(t, os.RemoveAll(filepath.Dir(build)))
		refused(t, id)
		if _, err := os.Stat(filepath.Dir(build)); !os.IsNotExist(err) {
			t.Error("a download created the build folder")
		}
	})

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, build string)
	}{
		{"outputs are links", func(t *testing.T, build string) {
			for _, n := range []string{"main.pdf", "main.log"} {
				must(t, os.Remove(filepath.Join(build, n)))
				link(t, filepath.Join(outside, n), filepath.Join(build, n))
			}
		}},
		{"build folder is a link", func(t *testing.T, build string) {
			must(t, os.RemoveAll(build))
			link(t, outside, build)
		}},
		{".refract is a link", func(t *testing.T, build string) {
			must(t, os.RemoveAll(filepath.Dir(build)))
			link(t, outside, filepath.Dir(build))
		}},
		{"outputs replaced by folders", func(t *testing.T, build string) {
			for _, n := range []string{"main.pdf", "main.log"} {
				must(t, os.Remove(filepath.Join(build, n)))
				must(t, os.Mkdir(filepath.Join(build, n), 0o755))
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, build := project(t)
			tc.setup(t, build)
			refused(t, id)
		})
	}

	// The build folder replaced between two downloads: the second request
	// checks again and serves nothing from outside.
	t.Run("build folder replaced after a download", func(t *testing.T) {
		id, build := project(t)
		if w := get(id, "pdf", ""); w.Code != http.StatusOK || w.Body.String() != "%PDF-1.7 built" {
			t.Fatalf("before: %d %q", w.Code, w.Body)
		}
		must(t, os.Rename(build, build+".old"))
		link(t, outside, build)
		refused(t, id)
	})
}

func TestCompileActivity(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "tectonic")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'note: downloading siunitx.sty'\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	runner := &compile.Runner{Compiler: &compile.Tectonic{Bin: bin, Timeout: time.Minute}}
	mux := http.NewServeMux()
	(&compileAPI{svc: svc, runner: runner}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})

	ana := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	bob := auth.WithUser(context.Background(), auth.User{ID: "bob"})
	p, err := svc.Create(ana, "Paper")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.WriteFile(ana, p.ID, "main.tex", strings.NewReader(`\documentclass{article}`)); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetMainFile(ana, p.ID, "main.tex"); err != nil {
		t.Fatal(err)
	}
	get := func(ctx context.Context) (int, compile.Activity) {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/api/projects/"+p.ID+"/compile/activity", nil).WithContext(ctx))
		var a compile.Activity
		json.Unmarshal(w.Body.Bytes(), &a)
		return w.Code, a
	}
	if code, a := get(ana); code != 200 || a.State != "idle" {
		t.Fatalf("idle: %d %+v", code, a)
	}
	ctx, cancel := context.WithCancel(ana)
	done := make(chan struct{})
	go func() {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", "/api/projects/"+p.ID+"/compile", nil).WithContext(ctx))
		close(done)
	}()
	var a compile.Activity
	for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if _, a = get(ana); a.State == "downloading" {
			break
		}
	}
	if a.State != "downloading" || a.File != "siunitx.sty" || a.Downloaded != 1 {
		t.Fatalf("during: %+v", a)
	}
	if code, _ := get(bob); code != http.StatusNotFound {
		t.Errorf("another user's project: %d, want 404", code)
	}
	cancel()
	<-done
	if _, a := get(ana); a.State != "idle" {
		t.Errorf("after: %+v", a)
	}
}
