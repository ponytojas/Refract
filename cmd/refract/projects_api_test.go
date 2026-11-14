package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/refs"
	"github.com/ponytojas/Refract/internal/store"
)

func TestProjectSymbols(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	mux := http.NewServeMux()
	(&projectsAPI{svc: svc}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})

	as := func(user string) context.Context { return auth.WithUser(context.Background(), auth.User{ID: user}) }
	p, err := svc.Create(as("ana"), "Paper")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"chapter.tex": `\label{sec:intro}`,
		"refs.bib":    `@book{knuth1984, title={TeX}}`,
	} {
		if err := svc.WriteFile(as("ana"), p.ID, name, strings.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	get := func(user string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/projects/"+p.ID+"/symbols", nil).WithContext(as(user))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}

	w := get("ana")
	if w.Code != http.StatusOK {
		t.Fatalf("owner: status %d: %s", w.Code, w.Body)
	}
	var got refs.Symbols
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Labels, []string{"sec:intro"}) || !reflect.DeepEqual(got.Citations, []refs.BibEntry{{Key: "knuth1984", Title: "TeX"}}) {
		t.Fatalf("symbols %+v", got)
	}
	for _, f := range []string{"chapter.tex", "refs.bib"} {
		found := false
		for _, have := range got.Files {
			found = found || have == f
		}
		if !found {
			t.Fatalf("%s missing from %q", f, got.Files)
		}
	}

	if w := get("luis"); w.Code != http.StatusNotFound {
		t.Fatalf("another user: status %d", w.Code)
	}
}

// trashAPI serves the real project handlers through the route table.
func trashAPI(t *testing.T) (*projects.Service, func(user, method, path string) *httptest.ResponseRecorder) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	mux := http.NewServeMux()
	(&projectsAPI{svc: svc}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})
	return svc, func(user, method, path string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, nil).WithContext(auth.WithUser(context.Background(), auth.User{ID: user}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
}

func TestTrashFlow(t *testing.T) {
	svc, call := trashAPI(t)
	p, err := svc.Create(auth.WithUser(context.Background(), auth.User{ID: "ana"}), "Paper")
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/projects/" + p.ID
	status := func(w *httptest.ResponseRecorder, want int) {
		t.Helper()
		if w.Code != want {
			t.Fatalf("status %d, want %d: %s", w.Code, want, w.Body)
		}
	}

	status(call("ana", "DELETE", base), http.StatusNoContent)
	w := call("ana", "GET", "/api/projects")
	status(w, http.StatusOK)
	if strings.Contains(w.Body.String(), p.ID) {
		t.Errorf("the list still has it: %s", w.Body)
	}
	w = call("ana", "GET", "/api/trash")
	status(w, http.StatusOK)
	var trash struct {
		Days     int                       `json:"days"`
		Projects []projects.TrashedProject `json:"projects"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &trash); err != nil || trash.Days != 30 || len(trash.Projects) != 1 || trash.Projects[0].ID != p.ID {
		t.Fatalf("trash = %+v, %v", trash, err)
	}
	for _, path := range []string{base, base + "/export", base + "/files", base + "/chats"} {
		status(call("ana", "GET", path), http.StatusNotFound)
	}
	status(call("ben", "POST", base+"/restore"), http.StatusNotFound)
	w = call("ana", "POST", base+"/restore")
	status(w, http.StatusOK)
	if !strings.Contains(w.Body.String(), p.ID) {
		t.Errorf("restore answered %s", w.Body)
	}
	status(call("ana", "GET", base), http.StatusOK)
	status(call("ana", "DELETE", base+"/purge"), http.StatusNotFound) // live: never purged
	status(call("ana", "DELETE", base), http.StatusNoContent)
	status(call("ana", "DELETE", base+"/purge"), http.StatusNoContent)
	if _, err := os.Stat(svc.Path(p.ID)); !os.IsNotExist(err) {
		t.Errorf("the directory is still there: %v", err)
	}
}

func TestTrashRefusedWhileTurnRuns(t *testing.T) {
	svc, call := trashAPI(t)
	svc.HoldTurns = func(string) (func(), bool) { return nil, false }
	p, _ := svc.Create(auth.WithUser(context.Background(), auth.User{ID: "ana"}), "Paper")
	w := call("ana", "DELETE", "/api/projects/"+p.ID)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"kind":"project_busy"`) {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if call("ana", "GET", "/api/projects/"+p.ID).Code != http.StatusOK {
		t.Error("a refused trash changed the project")
	}
}
