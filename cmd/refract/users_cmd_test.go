package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/users"
)

func TestUsersRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFRACT_DATA_DIR", dir)
	t.Setenv("REFRACT_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		err := runUsers(args, &out)
		return out.String(), err
	}
	for _, email := range []string{"ana@example.com", "bob@example.com"} {
		if _, err := run("add", email); err != nil {
			t.Fatal(err)
		}
	}

	db, err := store.Open(filepath.Join(dir, "refract.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	people := &users.Service{DB: db}
	ana, _ := people.Resolve(ctx, "ana@example.com")
	bob, _ := people.Resolve(ctx, "bob@example.com")
	ps := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	mine, err := ps.Create(auth.WithUser(ctx, ana), "Thesis")
	if err != nil {
		t.Fatal(err)
	}
	theirs, _ := ps.Create(auth.WithUser(ctx, bob), "Poster")
	db.Exec(`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES ('c1', ?, 'chat', 0, 0)`, mine.ID)
	token, _ := people.NewSession(ctx, ana.ID)

	if _, err := run("signout", "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := people.Session(ctx, token); err == nil {
		t.Fatal("session survived signout")
	}

	out, err := run("remove", "ana@example.com")
	if err == nil || !strings.Contains(out, "Thesis") {
		t.Fatalf("remove without --yes: %q, %v", out, err)
	}
	if _, err := os.Stat(ps.Path(mine.ID)); err != nil {
		t.Fatal("removed without --yes")
	}

	if _, err := run("remove", "ana@example.com", "--yes"); err != nil {
		t.Fatal(err)
	}
	var chats, rows int
	db.QueryRow(`SELECT count(*) FROM chats`).Scan(&chats)
	db.QueryRow(`SELECT count(*) FROM projects WHERE id = ?`, mine.ID).Scan(&rows)
	if _, err := os.Stat(ps.Path(mine.ID)); !os.IsNotExist(err) || chats != 0 || rows != 0 {
		t.Fatalf("left behind: dir err %v, chats %d, rows %d", err, chats, rows)
	}
	if exists, _ := people.Exists(ctx, "ana@example.com"); exists {
		t.Fatal("account left")
	}
	if _, err := os.Stat(ps.Path(theirs.ID)); err != nil {
		t.Fatal("another user's project was deleted")
	}
	if _, err := run("remove", "nobody@example.com", "--yes"); err == nil {
		t.Fatal("removed a missing account")
	}
	if _, err := run("remove", "bob@example.com", "--force"); err == nil {
		t.Fatal("accepted an unknown flag")
	}
}
