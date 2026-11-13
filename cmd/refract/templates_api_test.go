package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

func TestTemplateAPIOwnershipAndCreation(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	mux := http.NewServeMux()
	(&projectsAPI{svc: svc}).routes(&router{mux: mux, acc: &access.Checker{DB: db}})
	call := func(user, method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body)).WithContext(auth.WithUser(context.Background(), auth.User{ID: user}))
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w
	}
	w := call("ana", "POST", "/api/projects", `{"name":"Article","template":"article"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var p projects.Project
	if err := json.Unmarshal(w.Body.Bytes(), &p); err != nil || p.MainFile != "main.tex" {
		t.Fatalf("project: %+v %v", p, err)
	}
	if w := call("ben", "POST", "/api/projects/"+p.ID+"/template", `{"name":"Stolen"}`); w.Code != http.StatusNotFound {
		t.Fatalf("foreign save: %d", w.Code)
	}
	w = call("ana", "POST", "/api/projects/"+p.ID+"/template", `{"name":"My article"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	var saved projects.Template
	if err := json.Unmarshal(w.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"name":"   "}`, `{"name":12}`, `{`, `{"name":"` + strings.Repeat("x", 5000) + `"}`} {
		if w := call("ana", "POST", "/api/projects/"+p.ID+"/template", body); w.Code != http.StatusBadRequest {
			t.Fatalf("invalid save input: %d", w.Code)
		}
	}
	for _, id := range []string{"missing-template", "../outside", "' OR 1=1 --", p.ID} {
		body, _ := json.Marshal(map[string]string{"name": "Invalid", "template": id})
		if w := call("ana", "POST", "/api/projects", string(body)); w.Code != http.StatusNotFound {
			t.Fatalf("invalid template %q: %d", id, w.Code)
		}
	}
	foreign := call("ben", "POST", "/api/projects", `{"name":"Copy","template":"`+saved.ID+`"}`)
	missing := call("ben", "POST", "/api/projects", `{"name":"Copy","template":"missing-template"}`)
	if foreign.Code != missing.Code || foreign.Body.String() != missing.Body.String() {
		t.Fatal("missing and inaccessible templates can be distinguished")
	}
	for user, want := range map[string]int{"ana": 6, "ben": 5} {
		w := call(user, "GET", "/api/templates", "")
		var templates []projects.Template
		if err := json.Unmarshal(w.Body.Bytes(), &templates); w.Code != http.StatusOK || err != nil || len(templates) != want {
			t.Fatalf("%s list: %d %+v %v", user, w.Code, templates, err)
		}
	}
	body := `{"name":"Copy","template":"` + saved.ID + `"}`
	for _, path := range []string{"/api/projects/" + saved.ID, "/api/projects/" + saved.ID + "/file?path=main.tex"} {
		if w := call("ben", "GET", path, ""); w.Code != http.StatusNotFound {
			t.Fatalf("foreign read: %d", w.Code)
		}
	}
	if w := call("ben", "POST", "/api/projects", body); w.Code != http.StatusNotFound {
		t.Fatalf("foreign instantiate: %d", w.Code)
	}
	if w := call("ben", "DELETE", "/api/projects/"+saved.ID, ""); w.Code != http.StatusNotFound {
		t.Fatalf("foreign delete: %d", w.Code)
	}
	w = call("ana", "POST", "/api/projects", body)
	if w.Code != http.StatusOK {
		t.Fatalf("instantiate: %d %s", w.Code, w.Body)
	}
	var copy projects.Project
	if err := json.Unmarshal(w.Body.Bytes(), &copy); err != nil {
		t.Fatal(err)
	}
	if w := call("ana", "DELETE", "/api/projects/"+saved.ID, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if w := call("ana", "GET", "/api/projects/"+copy.ID+"/file?path=main.tex", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `\documentclass`) {
		t.Fatalf("copy lost: %d", w.Code)
	}
	if w := call("", "GET", "/api/templates", ""); w.Code != http.StatusForbidden {
		t.Fatalf("anonymous list: %d", w.Code)
	}
}
