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
