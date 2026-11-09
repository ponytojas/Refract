package main

import (
	"errors"
	"github.com/ponytojas/Refract/internal/access"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ponytojas/Refract/internal/compile"
	"github.com/ponytojas/Refract/internal/projects"
)

type compileAPI struct {
	svc     *projects.Service
	runner  *compile.Runner
	synctex *compile.SyncTeX
}

func (a *compileAPI) routes(rt *router) {
	rt.project("POST /api/projects/{id}/compile", access.Owner, a.compile)
	rt.project("GET /api/projects/{id}/pdf", access.Owner, a.output(".pdf", "application/pdf"))
	rt.project("GET /api/projects/{id}/log", access.Owner, a.output(".log", "text/plain; charset=utf-8"))
	rt.project("GET /api/projects/{id}/synctex/forward", access.Owner, a.forward)
	rt.project("GET /api/projects/{id}/synctex/inverse", access.Owner, a.inverse)
}

// forward: ?file=&line=&col= → boxes on the PDF.
func (a *compileAPI) forward(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	q := r.URL.Query()
	line, _ := strconv.Atoi(q.Get("line"))
	col, _ := strconv.Atoi(q.Get("col"))
	boxes, err := a.synctex.Forward(r.Context(), a.svc.Path(p.ID), p.MainFile, q.Get("file"), line, col)
	if err != nil {
		syncError(w, err)
		return
	}
	writeJSON(w, map[string]any{"boxes": boxes})
}

// inverse: ?page=&x=&y= (PDF points from the top-left) → source file and line.
func (a *compileAPI) inverse(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	x, _ := strconv.ParseFloat(q.Get("x"), 64)
	y, _ := strconv.ParseFloat(q.Get("y"), 64)
	file, line, err := a.synctex.Inverse(r.Context(), a.svc.Path(p.ID), p.MainFile, page, x, y)
	if err != nil {
		syncError(w, err)
		return
	}
	writeJSON(w, map[string]any{"file": file, "line": line})
}

func syncError(w http.ResponseWriter, err error) {
	if errors.Is(err, compile.ErrNoSync) {
		httpError(w, err, http.StatusNotFound)
		return
	}
	slog.Error("synctex", "err", err)
	httpError(w, err, http.StatusInternalServerError)
}

// compile builds the main file. A successful build triggered by the user is
// a checkpoint commit (the AI's compiles in M6 call the Compiler directly).
func (a *compileAPI) compile(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	if p.MainFile == "" {
		httpError(w, errors.New("choose the main file first"), http.StatusBadRequest)
		return
	}
	res, err := a.runner.Compile(r.Context(), p.ID, a.svc.Path(p.ID), p.MainFile)
	if err != nil {
		slog.Error("compile", "project", p.ID, "err", err)
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	committed := false
	if res.OK {
		if committed, err = a.svc.Checkpoint(r.Context(), p.ID, "checkpoint: compile"); err != nil {
			slog.Error("checkpoint", "project", p.ID, "err", err)
		}
	}
	writeJSON(w, struct {
		*compile.Result
		Checkpoint bool `json:"checkpoint"`
	}{res, committed})
}

// output serves the last build's PDF or log (Range requests work: PDF.js uses them).
func (a *compileAPI) output(ext, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := a.svc.Get(r.Context(), r.PathValue("id"))
		if err != nil {
			projectError(w, err)
			return
		}
		base := path.Base(p.MainFile)
		name := strings.TrimSuffix(base, path.Ext(base)) + ext
		f, err := os.Open(filepath.Join(a.svc.Path(p.ID), filepath.FromSlash(compile.BuildDir), name))
		if err != nil {
			httpError(w, errors.New("not built yet"), http.StatusNotFound)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("Cache-Control", "no-store")
		http.ServeContent(w, r, name, info.ModTime(), f)
	}
}
