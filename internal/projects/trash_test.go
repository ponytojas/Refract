package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
)

var day0 = time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

// trashService is a service whose clock the test moves.
func trashService(t *testing.T) (*Service, *time.Time) {
	t.Helper()
	s := newService(t)
	s.Key = make([]byte, 32)
	now := day0
	s.Now = func() time.Time { return now }
	return s, &now
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

func rowCount(t *testing.T, s *Service, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTrashAndRestore(t *testing.T) {
	s, now := trashService(t)
	p, err := s.Create(ownerCtx, "paper")
	if err != nil {
		t.Fatal(err)
	}
	id := p.ID
	old := p.UpdatedAt.Unix()
	if err := s.SetRemote(ownerCtx, id, Remote{URL: "https://example.com/x.git", Token: "secret"}, true); err != nil {
		t.Fatal(err)
	}
	*now = day0
	if err := s.Trash(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ownerCtx); len(list) != 0 {
		t.Errorf("List = %v", list)
	}
	if _, err := s.Get(ownerCtx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get = %v", err)
	}
	if _, err := s.ListFiles(ownerCtx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("ListFiles = %v", err)
	}
	if _, _, err := s.ReadFile(ownerCtx, id, "main.tex"); !errors.Is(err, ErrNotFound) {
		t.Errorf("ReadFile = %v", err)
	}
	if err := s.Export(ownerCtx, id, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("Export = %v", err)
	}
	if _, err := s.Remote(ownerCtx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remote = %v", err)
	}
	if !exists(s.Path(id)) {
		t.Error("the directory is gone")
	}
	trash, err := s.ListTrash(ownerCtx)
	if err != nil || len(trash) != 1 || trash[0].ID != id {
		t.Fatalf("ListTrash = %v, %v", trash, err)
	}
	if !trash[0].DeletedAt.Equal(day0) || !trash[0].PurgeAt.Equal(day0.Add(30*24*time.Hour)) {
		t.Errorf("deleted %v, purge %v", trash[0].DeletedAt, trash[0].PurgeAt)
	}
	ben := auth.WithUser(context.Background(), auth.User{ID: "ben"})
	if other, _ := s.ListTrash(ben); len(other) != 0 {
		t.Errorf("another user's trash = %v", other)
	}
	if other, _ := s.ListTrash(context.Background()); len(other) != 0 {
		t.Errorf("anonymous trash = %v", other)
	}
	if err := s.Trash(ownerCtx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Trash = %v", err)
	}

	// A live project with the same name doesn't block the restore.
	if _, err := s.Create(ownerCtx, "paper"); err != nil {
		t.Fatal(err)
	}
	if err := s.Restore(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ownerCtx, id)
	if err != nil || got.UpdatedAt.Unix() != old {
		t.Fatalf("restored: %+v, %v", got, err)
	}
	if list, _ := s.List(ownerCtx); len(list) != 2 {
		t.Errorf("List = %d projects", len(list))
	}
	if r, err := s.Remote(ownerCtx, id); err != nil || r.Token != "secret" {
		t.Errorf("remote after restore: %+v, %v", r, err)
	}
	if err := s.Restore(ownerCtx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Restore = %v", err)
	}
}

func TestPurge(t *testing.T) {
	s, _ := trashService(t)
	p, _ := s.Create(ownerCtx, "paper")
	id := p.ID
	if err := s.SetRemote(ownerCtx, id, Remote{URL: "https://example.com/x.git"}, true); err != nil {
		t.Fatal(err)
	}
	s.DB.Exec(`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES ('c1', ?, 'Chat', 0, 0)`, id)

	if err := s.Purge(ownerCtx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Purge of a live project = %v", err)
	}
	if _, err := s.Get(ownerCtx, id); err != nil || !exists(s.Path(id)) {
		t.Fatal("a live project was changed by Purge")
	}
	if err := s.Trash(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.Purge(ownerCtx, id); err != nil {
		t.Fatal(err)
	}
	if n := rowCount(t, s, `SELECT count(*) FROM projects WHERE id = ?`, id); n != 0 {
		t.Error("the row is still there")
	}
	if n := rowCount(t, s, `SELECT count(*) FROM chats WHERE project_id = ?`, id); n != 0 {
		t.Error("the chat is still there")
	}
	if n := rowCount(t, s, `SELECT count(*) FROM settings WHERE key = ?`, remoteKey(id)); n != 0 {
		t.Error("the remote setting is still there")
	}
	if exists(s.Path(id)) {
		t.Error("the directory is still there")
	}
	if left, _ := filepath.Glob(filepath.Join(s.Dir, ".purge-*")); len(left) != 0 {
		t.Errorf("left %v", left)
	}

	live, _ := s.Create(ownerCtx, "live")
	if err := s.Destroy(ownerCtx, live.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ownerCtx, live.ID); !errors.Is(err, ErrNotFound) || exists(s.Path(live.ID)) {
		t.Error("Destroy left a live project")
	}
}

func TestPurgeExpired(t *testing.T) {
	s, now := trashService(t)
	a, _ := s.Create(ownerCtx, "a")
	b, _ := s.Create(ownerCtx, "b")
	c, _ := s.Create(ownerCtx, "c")
	*now = day0
	s.Trash(ownerCtx, a.ID)
	s.Trash(ownerCtx, c.ID)
	*now = day0.Add(20 * 24 * time.Hour)
	s.Trash(ownerCtx, b.ID)
	*now = day0.Add(25 * 24 * time.Hour)
	if err := s.Restore(ownerCtx, c.ID); err != nil { // restored: never purged
		t.Fatal(err)
	}

	*now = day0.Add(30*24*time.Hour + time.Second)
	if n, err := s.PurgeExpired(context.Background()); n != 1 || err != nil {
		t.Fatalf("PurgeExpired = %d, %v", n, err)
	}
	if exists(s.Path(a.ID)) || rowCount(t, s, `SELECT count(*) FROM projects WHERE id = ?`, a.ID) != 0 {
		t.Error("a was not purged")
	}
	if !exists(s.Path(b.ID)) || !exists(s.Path(c.ID)) {
		t.Error("b or c was purged early")
	}
	*now = day0.Add(50 * 24 * time.Hour)
	if n, err := s.PurgeExpired(context.Background()); n != 1 || err != nil {
		t.Fatalf("PurgeExpired = %d, %v", n, err)
	}
	if exists(s.Path(b.ID)) {
		t.Error("b was not purged")
	}
	if _, err := s.Get(ownerCtx, c.ID); err != nil {
		t.Errorf("the restored project: %v", err)
	}
}

func TestPurgeHealsInterruptedPurges(t *testing.T) {
	s, _ := trashService(t)
	p, _ := s.Create(ownerCtx, "paper")
	s.Trash(ownerCtx, p.ID)
	// The server stopped after the rename, before the delete.
	if err := os.Rename(s.Path(p.ID), filepath.Join(s.Dir, ".purge-"+p.ID)); err != nil {
		t.Fatal(err)
	}
	// What a purge left behind, and a directory nobody owns.
	os.MkdirAll(filepath.Join(s.Dir, ".purge-orphan", "x"), 0o755)
	os.MkdirAll(filepath.Join(s.Dir, "plain"), 0o755)

	if _, err := s.PurgeExpired(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !exists(filepath.Join(s.Path(p.ID), ".git")) {
		t.Error("the trashed project was not put back")
	}
	if err := s.Restore(ownerCtx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ListFiles(ownerCtx, p.ID); err != nil {
		t.Errorf("restored project: %v", err)
	}
	if exists(filepath.Join(s.Dir, ".purge-orphan")) {
		t.Error("the orphan .purge directory stayed")
	}
	if !exists(filepath.Join(s.Dir, "plain")) {
		t.Error("a plain directory with no row was removed")
	}
}

func TestTrashRespectsTurnsAndRemoteLock(t *testing.T) {
	s, _ := trashService(t)
	s.HoldTurns = func(string) (func(), bool) { return nil, false }
	p, _ := s.Create(ownerCtx, "paper")
	id := p.ID
	if err := s.Trash(ownerCtx, id); !errors.Is(err, ErrTurnRunning) {
		t.Errorf("Trash = %v", err)
	}
	if err := s.Purge(ownerCtx, id); !errors.Is(err, ErrTurnRunning) {
		t.Errorf("Purge = %v", err)
	}
	if err := s.Destroy(ownerCtx, id); !errors.Is(err, ErrTurnRunning) {
		t.Errorf("Destroy = %v", err)
	}
	if _, err := s.Get(ownerCtx, id); err != nil || !exists(s.Path(id)) {
		t.Fatal("a refused call changed the project")
	}

	s.HoldTurns = nil
	unlock := lockRemote(id)
	var finished atomic.Bool
	done := make(chan error)
	go func() {
		err := s.Trash(ownerCtx, id)
		finished.Store(true)
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if finished.Load() {
		t.Fatal("Trash didn't wait for the remote lock")
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestPurgeTrashEvery(t *testing.T) {
	s, now := trashService(t)
	p, _ := s.Create(ownerCtx, "paper")
	s.Trash(ownerCtx, p.ID)
	*now = day0.Add(31 * 24 * time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.PurgeTrashEvery(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for exists(s.Path(p.ID)) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if exists(s.Path(p.ID)) {
		t.Error("not purged at start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Error("PurgeTrashEvery didn't return after cancel")
	}
}
