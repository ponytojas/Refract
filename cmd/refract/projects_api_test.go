package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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
	(&projectsAPI{svc: svc}).routes(mux)

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
