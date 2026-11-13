package main

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"github.com/ponytojas/Refract/internal/access"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/refs"
)

type projectsAPI struct {
	svc *projects.Service
	tmp string // where uploads are spooled before unzipping
}

func (a *projectsAPI) routes(rt *router) {
	rt.user("GET /api/projects", a.list)
	rt.user("POST /api/projects", a.create)
	rt.user("GET /api/templates", a.templates)
	rt.project("POST /api/projects/{id}/template", access.Owner, a.saveTemplate)
	rt.user("POST /api/projects/import", a.importZip)
	rt.user("POST /api/projects/import-folder", a.importFolder)
	rt.project("GET /api/projects/{id}", access.Owner, a.get)
	rt.project("PATCH /api/projects/{id}", access.Owner, a.update)
	rt.project("DELETE /api/projects/{id}", access.Owner, a.delete)
	rt.project("GET /api/projects/{id}/export", access.Owner, a.export)
	rt.project("GET /api/projects/{id}/checks", access.Owner, a.checks)
	rt.project("GET /api/projects/{id}/files", access.Owner, a.listFiles)
	rt.project("GET /api/projects/{id}/symbols", access.Owner, a.symbols)
	rt.project("GET /api/projects/{id}/file", access.Owner, a.readFile)
	rt.project("PUT /api/projects/{id}/file", access.Owner, a.writeFile)
	rt.project("DELETE /api/projects/{id}/file", access.Owner, a.removeFile)
	rt.project("POST /api/projects/{id}/mkdir", access.Owner, a.mkdir)
	rt.project("POST /api/projects/{id}/move", access.Owner, a.move)
	rt.project("GET /api/projects/{id}/history", access.Owner, a.history)
	rt.project("GET /api/projects/{id}/history/{hash}", access.Owner, a.commitDiff)
	rt.project("POST /api/projects/{id}/history/{hash}/restore", access.Owner, a.restore)
}

func (a *projectsAPI) history(w http.ResponseWriter, r *http.Request) {
	cs, err := a.svc.History(r.Context(), r.PathValue("id"), 300)
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, cs)
}

func (a *projectsAPI) commitDiff(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.CommitDiff(r.Context(), r.PathValue("id"), r.PathValue("hash"))
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, d)
}

// restore brings back one file (path set) or the whole project as of a
// commit, as a new commit. before restores a file as it was before the
// commit (to undo a deletion).
func (a *projectsAPI) restore(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path   string `json:"path"`
		Before bool   `json:"before"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	id, rev := r.PathValue("id"), r.PathValue("hash")
	var commit string
	var err error
	if in.Path == "" {
		commit, err = a.svc.RestoreProject(r.Context(), id, rev)
	} else {
		if in.Before {
			rev += "^"
		}
		commit, err = a.svc.RestoreFile(r.Context(), id, rev, in.Path)
	}
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, map[string]string{"commit": commit})
}

func (a *projectsAPI) listFiles(w http.ResponseWriter, r *http.Request) {
	files, err := a.svc.ListFiles(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, files)
}

// readFile returns raw bytes; X-Refract-Text tells the editor whether to open it as text.
// symbols feeds the editor's completion: labels, citation keys and files.
func (a *projectsAPI) symbols(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	root, err := os.OpenRoot(a.svc.Path(p.ID))
	if err != nil {
		projectError(w, err)
		return
	}
	defer root.Close()
	writeJSON(w, refs.ScanSymbols(root.FS()))
}

func (a *projectsAPI) readFile(w http.ResponseWriter, r *http.Request) {
	b, text, err := a.svc.ReadFile(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		projectError(w, err)
		return
	}
	ct := "text/plain; charset=utf-8"
	if !text {
		ct = http.DetectContentType(b)
		if strings.HasSuffix(strings.ToLower(r.URL.Query().Get("path")), ".svg") {
			ct = "image/svg+xml"
		}
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Refract-Text", map[bool]string{true: "1", false: "0"}[text])
	// User content: never sniffed or rendered as an active document.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (a *projectsAPI) writeFile(w http.ResponseWriter, r *http.Request) {
	body := http.MaxBytesReader(w, r.Body, projects.MaxFileSize)
	if err := a.svc.WriteFile(r.Context(), r.PathValue("id"), r.URL.Query().Get("path"), body); err != nil {
		projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *projectsAPI) removeFile(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Remove(r.Context(), r.PathValue("id"), r.URL.Query().Get("path")); err != nil {
		projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *projectsAPI) mkdir(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Mkdir(r.Context(), r.PathValue("id"), r.URL.Query().Get("path")); err != nil {
		projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *projectsAPI) move(w http.ResponseWriter, r *http.Request) {
	var in struct{ From, To string }
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := a.svc.Move(r.Context(), r.PathValue("id"), in.From, in.To); err != nil {
		projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *projectsAPI) list(w http.ResponseWriter, r *http.Request) {
	ps, err := a.svc.List(r.Context())
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, ps)
}

func (a *projectsAPI) get(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, p)
}

func (a *projectsAPI) create(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     string `json:"name"`
		Template string `json:"template"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	var p *projects.Project
	var err error
	if in.Template != "" {
		p, err = a.svc.CreateFromTemplate(r.Context(), in.Name, in.Template)
	} else {
		p, err = a.svc.Create(r.Context(), in.Name)
	}
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, p)
}

func (a *projectsAPI) templates(w http.ResponseWriter, r *http.Request) {
	templates, err := a.svc.Templates(r.Context())
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, templates)
}

func (a *projectsAPI) saveTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		httpError(w, errors.New("template name required"), http.StatusBadRequest)
		return
	}
	template, err := a.svc.SaveTemplate(r.Context(), r.PathValue("id"), in.Name)
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, template)
}

// importZip takes the raw zip as the body; the project name comes from ?name=.
func (a *projectsAPI) importZip(w http.ResponseWriter, r *http.Request) {
	// Spool to disk: zip needs random access.
	if err := os.MkdirAll(a.tmp, 0o700); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	f, err := os.CreateTemp(a.tmp, "upload-*.zip")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err := io.Copy(f, http.MaxBytesReader(w, r.Body, a.svc.Limits.MaxBytes)); err != nil {
		httpError(w, errors.New("upload too large or interrupted"), http.StatusRequestEntityTooLarge)
		return
	}
	res, err := a.svc.ImportZip(r.Context(), strings.TrimSuffix(r.URL.Query().Get("name"), ".zip"), f.Name())
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, res)
}

// importFolder takes a multipart upload whose part filenames are relative paths.
func (a *projectsAPI) importFolder(w http.ResponseWriter, r *http.Request) {
	ct, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || ct != "multipart/form-data" {
		httpError(w, errors.New("multipart/form-data required"), http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.svc.Limits.MaxBytes+10<<20) // + multipart overhead
	mr := multipart.NewReader(r.Body, params["boundary"])
	res, err := a.svc.Import(r.Context(), r.URL.Query().Get("name"), projects.MultipartSource(mr))
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, res)
}

func (a *projectsAPI) update(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name     *string `json:"name"`
		MainFile *string `json:"main_file"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	if in.Name != nil {
		if err := a.svc.Rename(r.Context(), id, *in.Name); err != nil {
			projectError(w, err)
			return
		}
	}
	if in.MainFile != nil {
		if err := a.svc.SetMainFile(r.Context(), id, *in.MainFile); err != nil {
			projectError(w, err)
			return
		}
	}
	p, err := a.svc.Get(r.Context(), id)
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, p)
}

func (a *projectsAPI) delete(w http.ResponseWriter, r *http.Request) {
	if err := a.svc.Delete(r.Context(), r.PathValue("id")); err != nil {
		projectError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *projectsAPI) export(w http.ResponseWriter, r *http.Request) {
	p, err := a.svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": p.Name + ".zip"}))
	if err := a.svc.Export(r.Context(), p.ID, w); err != nil {
		// Headers are gone; the truncated zip fails to open, which is the signal.
		panic(http.ErrAbortHandler)
	}
}

func (a *projectsAPI) checks(w http.ResponseWriter, r *http.Request) {
	ws, err := a.svc.Check(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, ws)
}

func projectError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, projects.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.Is(err, access.ErrNotFound):
		httpError(w, err, http.StatusNotFound)
	case errors.Is(err, access.ErrForbidden), errors.Is(err, access.ErrNoUser):
		httpError(w, err, http.StatusForbidden)
	case errors.Is(err, projects.ErrNoRemote):
		httpError(w, err, http.StatusNotFound)
	case errors.Is(err, projects.ErrBehind):
		httpError(w, err, http.StatusConflict)
	case errors.Is(err, projects.ErrBadRemote), errors.Is(err, projects.ErrPrivateRemote):
		httpError(w, err, http.StatusBadRequest)
	case errors.Is(err, fs.ErrExist):
		httpError(w, err, http.StatusConflict)
	case errors.Is(err, projects.ErrUnsafePath), errors.Is(err, projects.ErrBadPath), errors.Is(err, projects.ErrBadRevision), errors.Is(err, projects.ErrTooLarge), errors.Is(err, zip.ErrFormat):
		httpError(w, err, http.StatusBadRequest)
	default:
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpError(w, errors.New("upload too large"), http.StatusRequestEntityTooLarge)
			return
		}
		slog.Error("projects", "err", err)
		httpError(w, err, http.StatusInternalServerError)
	}
}
