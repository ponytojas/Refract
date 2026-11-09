package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

// Auto-push acts as the user who committed: it pushes only while they own
// the project.
func TestAutoPushRunsAsTheCommitter(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits, Key: make([]byte, 32)}
	ana := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	ben := auth.WithUser(context.Background(), auth.User{ID: "ben"})
	p, err := svc.Create(ana, "Paper")
	if err != nil {
		t.Fatal(err)
	}
	// Nothing listens there: a push fails fast, but it is attempted and recorded.
	if err := svc.SetRemote(ana, p.ID, projects.Remote{URL: "https://127.0.0.1:1/paper.git", Branch: "main", AutoPush: true}, false); err != nil {
		t.Fatal(err)
	}
	a := &remoteAPI{svc: svc, db: db, acc: &access.Checker{DB: db}}

	for name, ctx := range map[string]context.Context{"another user": ben, "no user": context.Background()} {
		a.AutoPush(ctx, p.ID)
		if _, pushed := a.last[p.ID]; pushed {
			t.Fatalf("%s: auto-push ran", name)
		}
	}
	a.AutoPush(ana, p.ID)
	if st, pushed := a.last[p.ID]; !pushed || st.Op != "push" || !st.Auto {
		t.Fatalf("owner: %+v, %v", st, pushed)
	}
}
