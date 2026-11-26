package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/accountexport"
	"github.com/ponytojas/Refract/internal/projects"
)

const exportRetention = 10 * time.Minute

type accountExport struct {
	status     string // preparing, ready or failed
	cancel     context.CancelFunc
	timer      *time.Timer
	path       string
	downloaded bool
}

// accountExports keeps in-progress archives off request threads and finished
// archives on disk. The map is indexed by the authenticated account ID.
type accountExports struct {
	ctx      context.Context
	projects *projects.Service
	mu       sync.Mutex
	jobs     map[string]*accountExport
	running  int
	closed   bool
}

func (e *accountExports) start(owner string) (string, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.ctx.Err() != nil {
		return "", false
	}
	existing := e.jobs[owner]
	if existing != nil && (existing.status == "preparing" || existing.status == "ready" && !existing.downloaded) {
		return existing.status, true
	}
	// A small number of simultaneous git packs keeps a shared server responsive.
	if e.running >= 2 {
		return "", false
	}
	e.removeLocked(owner)
	if e.jobs == nil {
		e.jobs = make(map[string]*accountExport)
	}
	ctx, cancel := context.WithTimeout(e.ctx, 30*time.Minute)
	job := &accountExport{status: "preparing", cancel: cancel}
	e.jobs[owner] = job
	e.running++
	go e.build(ctx, owner, job)
	return job.status, true
}

func (e *accountExports) build(ctx context.Context, owner string, job *accountExport) {
	defer job.cancel()
	f, err := os.CreateTemp("", "refract-account-export-*.zip")
	var path string
	if err == nil {
		path = f.Name()
		err = errors.Join(accountexport.Write(ctx, f, e.projects, owner), f.Close(), ctx.Err())
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.running--
	if e.jobs[owner] != job {
		if path != "" {
			os.Remove(path)
		}
		return
	}
	if err != nil {
		if path != "" {
			os.Remove(path)
		}
		slog.Warn("account export failed", "user", owner, "err", err)
		job.status = "failed"
	} else {
		job.status, job.path = "ready", path
	}
	job.timer = time.AfterFunc(exportRetention, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.jobs[owner] == job {
			e.removeLocked(owner)
		}
	})
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
	if job.path != "" {
		os.Remove(job.path)
	}
}

func (e *accountExports) discard(owner string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.removeLocked(owner)
}

func (e *accountExports) close() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	for owner := range e.jobs {
		e.removeLocked(owner)
	}
}

func (e *accountExports) routes(rt *router) {
	rt.user("POST /api/account/export", e.startExport)
	rt.user("GET /api/account/export", e.exportStatus)
	rt.user("GET /api/account/export/download", e.downloadExport)
}

func (e *accountExports) startExport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	status, ok := e.start(requestUser(r.Context()))
	if !ok {
		httpError(w, errors.New("account export is busy"), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	writeJSON(w, map[string]string{"status": status})
}

func (e *accountExports) exportStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	e.mu.Lock()
	var status string
	if job := e.jobs[requestUser(r.Context())]; job != nil {
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
	job := e.jobs[requestUser(r.Context())]
	if job == nil || job.status != "ready" {
		e.mu.Unlock()
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(job.path)
	if err == nil && r.Method != http.MethodHead {
		job.downloaded = true
	}
	e.mu.Unlock()
	if err != nil {
		httpError(w, errors.New("account export is unavailable"), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="refract-account.zip"`)
	http.ServeContent(w, r, "refract-account.zip", time.Time{}, f)
}
