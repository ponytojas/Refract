package projects

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// edit writes a file in the working tree and checkpoints it as one commit.
func edit(t *testing.T, s *Service, id, rel, body, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(s.Path(id), rel), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoint(ownerCtx, id, message); err != nil {
		t.Fatal(err)
	}
}

func TestHistoryAndCommitDiff(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	edit(t, s, id, "intro.tex", "changed\n", "Edit intro")

	hs, err := s.History(ctx, id, 50)
	if err != nil || len(hs) != 2 || hs[0].Message != "Edit intro" || len(hs[0].Short) < 7 || hs[0].Time.IsZero() {
		t.Fatalf("history = %+v, %v", hs, err)
	}
	d, err := s.CommitDiff(ctx, id, hs[0].Hash)
	if err != nil || len(d) != 1 || d[0].Path != "intro.tex" || d[0].Status != "modified" || d[0].Before != intro || d[0].After != "changed\n" {
		t.Fatalf("diff = %+v, %v", d, err)
	}
	// The first commit diffs against nothing.
	d, err = s.CommitDiff(ctx, id, hs[1].Hash)
	if err != nil || len(d) != 2 || d[0].Status != "added" || d[0].Before != "" {
		t.Fatalf("first diff = %+v, %v", d, err)
	}
	for _, bad := range []string{"HEAD", "--all", "abc", hs[0].Hash + "^", "../x"} {
		if _, err := s.CommitDiff(ctx, id, bad); !errors.Is(err, ErrBadRevision) {
			t.Errorf("CommitDiff(%q) = %v", bad, err)
		}
	}
}

func TestRestoreFileAddsACommit(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	first, _ := s.History(ctx, id, 1)
	edit(t, s, id, "intro.tex", "changed\n", "Edit intro")
	// Unsaved-to-git edits are checkpointed, not lost.
	os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("pending\n"), 0o644)
	n := commitCount(t, s, id)

	c, err := s.RestoreFile(ctx, id, first[0].Hash, "intro.tex")
	if err != nil || c == "" {
		t.Fatalf("restore = %q, %v", c, err)
	}
	if got := read(t, s.Path(id), "intro.tex"); got != intro {
		t.Errorf("intro = %q", got)
	}
	if got := read(t, s.Path(id), "main.tex"); got != "pending\n" {
		t.Errorf("main.tex = %q, the pending edit was lost", got)
	}
	if got := commitCount(t, s, id); got != n+2 { // checkpoint + restore
		t.Errorf("commits = %d, want %d", got, n+2)
	}
	// Same content again: nothing to commit.
	if c, err := s.RestoreFile(ctx, id, first[0].Hash, "intro.tex"); err != nil || c != "" {
		t.Errorf("second restore = %q, %v", c, err)
	}
	// A file absent at that version is removed; one deleted later comes back from its parent.
	edit(t, s, id, "notes.tex", "notes\n", "Add notes")
	if _, err := s.RestoreFile(ctx, id, first[0].Hash, "notes.tex"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(s.Path(id), "notes.tex")); !os.IsNotExist(err) {
		t.Errorf("notes.tex still there: %v", err)
	}
	hs, _ := s.History(ctx, id, 1)
	if _, err := s.RestoreFile(ctx, id, hs[0].Hash+"^", "notes.tex"); err != nil || read(t, s.Path(id), "notes.tex") != "notes\n" {
		t.Errorf("restore deleted file: %v", err)
	}
	if _, err := s.RestoreFile(ctx, id, first[0].Hash, "../etc/passwd"); err == nil {
		t.Error("path outside the project accepted")
	}
}

func TestRestoreProject(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	first, _ := s.History(ctx, id, 1)
	edit(t, s, id, "intro.tex", "changed\n", "Edit intro")
	edit(t, s, id, "notes.tex", "notes\n", "Add notes")
	n := commitCount(t, s, id)

	if c, err := s.RestoreProject(ctx, id, first[0].Hash); err != nil || c == "" {
		t.Fatalf("restore = %q, %v", c, err)
	}
	if got := read(t, s.Path(id), "intro.tex"); got != intro {
		t.Errorf("intro = %q", got)
	}
	if _, err := os.Stat(filepath.Join(s.Path(id), "notes.tex")); !os.IsNotExist(err) {
		t.Error("a file added after that version survived")
	}
	if got := commitCount(t, s, id); got != n+1 {
		t.Errorf("commits = %d, want %d (history kept, one new commit)", got, n+1)
	}
}
