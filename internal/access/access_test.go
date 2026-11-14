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

func TestTrashedProjectsHaveNoRole(t *testing.T) {
	c := setup(t)
	if _, err := c.DB.Exec(`UPDATE projects SET deleted_at = 5 WHERE id = 'p1'`); err != nil {
		t.Fatal(err)
	}
	for _, ctx := range []context.Context{as("ana"), as("ben"), context.Background()} {
		if role, err := c.ProjectRole(ctx, "p1"); role != None || err != nil {
			t.Errorf("ProjectRole = %v, %v; want None", role, err)
		}
		if err := c.Require(ctx, "p1", Viewer); !errors.Is(err, ErrNotFound) {
			t.Errorf("Require = %v, want ErrNotFound", err)
		}
	}
	// A trashed project's chat and turn lead to it, and Require then refuses.
	p, err := c.ChatProject(context.Background(), "c1")
	if err != nil || !errors.Is(c.Require(as("ana"), p, Viewer), ErrNotFound) {
		t.Errorf("chat of a trashed project: %q, %v", p, err)
	}
	if role, err := c.TrashedRole(as("ana"), "p1"); role != Owner || err != nil {
		t.Errorf("TrashedRole owner = %v, %v", role, err)
	}
	for _, ctx := range []context.Context{as("ben"), context.Background()} {
		if role, err := c.TrashedRole(ctx, "p1"); role != None || err != nil {
			t.Errorf("TrashedRole stranger = %v, %v", role, err)
		}
	}
	if err := c.RequireTrashed(as("ben"), "p1", Owner); !errors.Is(err, ErrNotFound) {
		t.Errorf("RequireTrashed stranger = %v", err)
	}
	if err := c.RequireTrashed(as("ana"), "p1", Owner); err != nil {
		t.Errorf("RequireTrashed owner = %v", err)
	}
	// A live project is not in the trash.
	if role, err := c.TrashedRole(as(""), "old"); role != None || err != nil {
		t.Errorf("TrashedRole of a live project = %v, %v", role, err)
	}
	if _, err := c.DB.Exec(`UPDATE projects SET deleted_at = 0`); err != nil {
		t.Fatal(err)
	}
	if role, _ := c.TrashedRole(as("ana"), "p1"); role != None {
		t.Errorf("TrashedRole of a live project = %v", role)
	}
}
