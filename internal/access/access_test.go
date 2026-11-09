package access

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

func setup(t *testing.T) *Checker {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for _, q := range []string{
		`INSERT INTO projects (id, name, created_at, updated_at, owner) VALUES ('p1', 'Ana''s', 0, 0, 'ana'), ('old', 'Before accounts', 0, 0, '')`,
		`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES ('c1', 'p1', 'Chat', 0, 0)`,
		`INSERT INTO turns (id, chat_id, message, status, created_at, updated_at) VALUES ('t1', 'c1', 'Hi', 'review', 0, 0)`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return &Checker{DB: db}
}

func as(id string) context.Context { return auth.WithUser(context.Background(), auth.User{ID: id}) }

func TestProjectRole(t *testing.T) {
	c := setup(t)
	for _, tc := range []struct {
		name    string
		ctx     context.Context
		project string
		role    Role
		err     error
	}{
		{"owner", as("ana"), "p1", Owner, nil},
		{"stranger", as("ben"), "p1", None, ErrNotFound},
		{"anonymous", context.Background(), "p1", None, ErrNotFound},
		{"empty user id on an ownerless project", as(""), "old", None, ErrNotFound},
		{"unknown project", as("ana"), "nope", None, ErrNotFound},
	} {
		role, err := c.ProjectRole(tc.ctx, tc.project)
		if err != nil || role != tc.role {
			t.Errorf("%s: role = %v, %v; want %v", tc.name, role, err, tc.role)
		}
		if err := c.Require(tc.ctx, tc.project, Owner); !errors.Is(err, tc.err) {
			t.Errorf("%s: Require = %v, want %v", tc.name, err, tc.err)
		}
	}
}

// A viewer (T6a) can read but not ask for more; nobody below Viewer learns the project exists.
func TestAllow(t *testing.T) {
	for _, tc := range []struct {
		role, min Role
		err       error
	}{
		{Viewer, Viewer, nil},
		{Viewer, Owner, ErrForbidden},
		{Editor, Owner, ErrForbidden},
		{Owner, Viewer, nil},
		{None, Viewer, ErrNotFound},
	} {
		if err := allow(tc.role, tc.min); !errors.Is(err, tc.err) {
			t.Errorf("allow(%v, %v) = %v, want %v", tc.role, tc.min, err, tc.err)
		}
	}
}

func TestChatAndTurnProject(t *testing.T) {
	c := setup(t)
	ctx := context.Background()
	if p, err := c.ChatProject(ctx, "c1"); p != "p1" || err != nil {
		t.Errorf("chat: %q, %v", p, err)
	}
	if p, err := c.TurnProject(ctx, "t1"); p != "p1" || err != nil {
		t.Errorf("turn: %q, %v", p, err)
	}
	if _, err := c.ChatProject(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown chat: %v", err)
	}
	if _, err := c.TurnProject(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown turn: %v", err)
	}
}
