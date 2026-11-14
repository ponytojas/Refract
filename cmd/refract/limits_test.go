package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/users"
)

func TestLimitsOf(t *testing.T) {
	for _, plan := range []string{"free", "plus", "ai"} {
		if limitsOf(plan) != plans[plan] {
			t.Errorf("limitsOf(%q) = %+v", plan, limitsOf(plan))
		}
	}
	if limitsOf("plus") != limitsOf("ai") {
		t.Error("plus and ai differ")
	}
	for _, plan := range []string{"", "pro", "FREE"} {
		if limitsOf(plan) != plans["free"] {
			t.Errorf("limitsOf(%q) = %+v, want free", plan, limitsOf(plan))
		}
	}
}

func TestPlanOf(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.Exec(`INSERT INTO users (id, email, created_at) VALUES ('u1', 'a@example.com', 1)`); err != nil {
		t.Fatal(err)
	}
	if p, err := planOf(ctx, db, "u1"); err != nil || p != "free" {
		t.Fatalf("new account: %q, %v", p, err)
	}
	if err := (&users.Service{DB: db}).SetPlan(ctx, "A@example.com", "plus"); err != nil {
		t.Fatal(err)
	}
	if p, _ := planOf(ctx, db, "u1"); p != "plus" {
		t.Fatalf("after SetPlan: %q", p)
	}
	if p, err := planOf(ctx, db, "nobody"); err != nil || p != "free" {
		t.Fatalf("unknown id: %q, %v", p, err)
	}
}

func TestUsersPlan(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFRACT_DATA_DIR", dir)
	t.Setenv("REFRACT_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runUsers(args, &out)
		return out.String(), err
	}
	if _, err := run("add", "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	plan := func() string {
		db, err := store.Open(filepath.Join(dir, "refract.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var p string
		db.QueryRow(`SELECT plan FROM users WHERE email = 'ana@example.com'`).Scan(&p)
		return p
	}
	if out, err := run("plan", "ana@example.com", "plus"); err != nil || !strings.Contains(out, "ana@example.com is now on the plus plan") {
		t.Fatalf("plan: %q, %v", out, err)
	}
	if plan() != "plus" {
		t.Fatalf("plan = %q", plan())
	}
	if _, err := run("plan", "ana@example.com", "gold"); err == nil || !strings.Contains(err.Error(), "ai, free, plus") {
		t.Fatalf("unknown plan: %v", err)
	}
	if _, err := run("plan", "nobody@example.com", "ai"); err == nil || !strings.Contains(err.Error(), "there is no account") {
		t.Fatalf("unknown email: %v", err)
	}
	if plan() != "plus" {
		t.Fatalf("plan changed to %q", plan())
	}
}

func TestOverLimitIsForbidden(t *testing.T) {
	acc := accessDB(t)
	ps := &projects.Service{DB: acc.DB, Dir: t.TempDir()}
	ps.QuotaFor = func(context.Context, string) (projects.Quota, error) { return projects.Quota{MaxProjects: 1}, nil }
	acc.DB.Exec(`DELETE FROM projects WHERE owner = 'ben'`)
	rt := &router{mux: http.NewServeMux(), acc: acc}
	(&projectsAPI{svc: ps}).routes(rt)
	post := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/projects", strings.NewReader(`{"name":"p"}`))
		rt.mux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "ben"})))
		return w
	}
	if w := post(); w.Code != http.StatusOK {
		t.Fatalf("first: %d %s", w.Code, w.Body)
	}
	if w := post(); w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"kind":"over_projects"`) {
		t.Fatalf("second: %d %s", w.Code, w.Body)
	}
}
