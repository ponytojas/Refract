package main

import (
	"context"
	"crypto/rand"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/accountexport"
	"github.com/ponytojas/Refract/internal/projects"
)

const (
	exportRetention = 10 * time.Minute
	exportDirPrefix = "refract-account-export-"
)

type accountExport struct {
	id      string
	status  string // preparing, ready or failed
	ctx     context.Context
	cancel  context.CancelFunc
	timer   *time.Timer
	dir     string
	readers int
}

// accountExports keeps at most one current request per account. A newer
// request for an account reuses its worker slot after the old build stops.
type accountExports struct {
	ctx      context.Context
	projects *projects.Service
	tmp      string
	mu       sync.Mutex
	jobs     map[string]*accountExport
	workers  map[string]bool
	builds   sync.WaitGroup
	closed   bool
}

func newAccountExports(ctx context.Context, ps *projects.Service, tmp string) *accountExports {
	e := &accountExports{
		ctx: ctx, projects: ps, tmp: filepath.Join(tmp, "account-exports"),
		jobs: make(map[string]*accountExport), workers: make(map[string]bool),
	}
	entries, err := os.ReadDir(e.tmp)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("could not list account export staging files", "err", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), exportDirPrefix) {
			cleanExportDir(filepath.Join(e.tmp, entry.Name()))
		}
	}
	return e
}

func cleanExportDir(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		slog.Warn("could not remove account export staging files", "err", err)
	}
}

func (e *accountExports) start(owner string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil || (!e.workers[owner] && len(e.workers) >= 2) {
		return ""
	}
	e.removeLocked(owner)
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Minute)
	job := &accountExport{id: rand.Text(), status: "preparing", ctx: ctx, cancel: cancel}
	e.jobs[owner] = job
	if !e.workers[owner] {
		e.workers[owner] = true
		e.builds.Add(1)
		go e.run(owner, job)
	}
	return job.id
}

func (e *accountExports) run(owner string, job *accountExport) {
	defer e.builds.Done()
	for {
		dir, err := e.build(owner, job.ctx)
		job.cancel()
		e.mu.Lock()
		current := e.jobs[owner]
		if current != job {
			e.mu.Unlock()
			if dir != "" {
				cleanExportDir(dir)
			}
			e.mu.Lock()
			job = e.jobs[owner]
			if job != nil {
				e.mu.Unlock()
				continue
			}
		} else {
			if err != nil {
				slog.Warn("account export failed", "user", owner, "err", err)
				job.status = "failed"
			} else {
				job.status, job.dir = "ready", dir
			}
			job.timer = time.AfterFunc(exportRetention, func() {
				e.mu.Lock()
				defer e.mu.Unlock()
				if e.jobs[owner] == job {
					e.removeLocked(owner)
				}
			})
		}
		delete(e.workers, owner)
		e.mu.Unlock()
		return
	}
}

func (e *accountExports) build(owner string, ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(e.tmp, 0o700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(e.tmp, exportDirPrefix)
	if err != nil {
		return "", err
	}
	f, err := os.OpenFile(filepath.Join(dir, "account.zip"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		err = errors.Join(accountexport.Write(ctx, f, e.projects, owner, dir), f.Close(), ctx.Err())
	}
	if err != nil {
		cleanExportDir(dir)
		return "", err
	}
	return dir, nil
}

func (e *accountExports) removeLocked(owner string) {
	job := e.jobs[owner]
	if job == nil {
		return
	}
	delete(e.jobs, owner)
	job.cancel()
	if job.timer != nil {
		job.timer.Stop()
	}
	if job.dir != "" && job.readers == 0 {
		cleanExportDir(job.dir)
	}
}

func (e *accountExports) discard(owner string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.removeLocked(owner)
}

func (e *accountExports) close() {
	e.mu.Lock()
	e.closed = true
	for owner := range e.jobs {
		e.removeLocked(owner)
	}
	e.mu.Unlock()
	e.builds.Wait()
}

func (e *accountExports) routes(rt *router) {
	rt.user("POST /api/account/export", e.startExport)
	rt.user("GET /api/account/export/{id}", e.exportStatus)
	rt.user("GET /api/account/export/{id}/download", e.downloadExport)
}

func (e *accountExports) startExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id := e.start(requestUser(r.Context()))
	if id == "" {
		httpError(w, errors.New("account export is busy"), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"id": id, "status": "preparing"})
}

func (e *accountExports) exportStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	e.mu.Lock()
	var status string
	if job := e.jobs[requestUser(r.Context())]; job != nil && job.id == r.PathValue("id") {
		status = job.status
	}
	e.mu.Unlock()
	if status == "" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]string{"status": status})
}

func (e *accountExports) downloadExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	e.mu.Lock()
	owner := requestUser(r.Context())
	job := e.jobs[owner]
	if job == nil || job.id != r.PathValue("id") || job.status != "ready" {
		e.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(job.dir, "account.zip"))
	if err == nil {
		job.readers++
	}
	e.mu.Unlock()
	if err != nil {
		httpError(w, errors.New("account export is unavailable"), http.StatusInternalServerError)
		return
	}
	defer func() {
		f.Close()
		e.mu.Lock()
		defer e.mu.Unlock()
		job.readers--
		if job.readers == 0 && e.jobs[owner] != job {
			cleanExportDir(job.dir)
		}
	}()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="refract-account.zip"`)
	http.ServeContent(w, r, "refract-account.zip", time.Time{}, f)
}
