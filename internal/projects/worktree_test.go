package projects

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const intro = "line 1\nline 2\nline 3\nline 4\nline 5\n"

func newTurnProject(t *testing.T) (*Service, string) {
	t.Helper()
	s := newService(t)
	res, err := s.Import(ownerCtx, "p", ZipSource(makeZip(t,
		entry{name: "main.tex", body: doc},
		entry{name: "intro.tex", body: intro},
	)))
	if err != nil {
		t.Fatal(err)
	}
	return s, res.Project.ID
}

func read(t *testing.T, dir, rel string) string {
	t.Helper()
	b, _ := os.ReadFile(filepath.Join(dir, rel))
	return string(b)
}

func commitCount(t *testing.T, s *Service, id string) int {
	t.Helper()
	out, err := git(ownerCtx, s.Path(id), "rev-list", "--count", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range strings.TrimSpace(out) {
		n = n*10 + int(c-'0')
	}
	return n
}

// aiTurn runs a turn that edits intro.tex line 2 and adds notes.tex.
func aiTurn(t *testing.T, s *Service, id, turn string) (base string) {
	t.Helper()
	ctx := ownerCtx
	base, dir, err := s.StartTurn(ctx, id, turn)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "intro.tex"), []byte(strings.Replace(intro, "line 2", "line 2 (AI)", 1)), 0o644)
	os.WriteFile(filepath.Join(dir, "notes.tex"), []byte("new\n"), 0o644)
	changes, err := s.FinishTurn(ctx, id, turn, base, "ai: work")
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 || changes[0] != (Change{"intro.tex", "modified"}) || changes[1] != (Change{"notes.tex", "added"}) {
		t.Fatalf("changes = %+v", changes)
	}
	// The user's copy is untouched while the turn waits for review.
	if read(t, s.Path(id), "intro.tex") != intro {
		t.Fatal("working copy changed before accept")
	}
	return base
}

func TestTurnAcceptAll(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	before := commitCount(t, s, id)
	base := aiTurn(t, s, id, "t1")

	old, new, err := s.TurnFile(ctx, id, "t1", base, "intro.tex")
	if err != nil || string(old) != intro || !strings.Contains(string(new), "(AI)") {
		t.Fatalf("TurnFile = %q / %q %v", old, new, err)
	}

	commit, conflicts, err := s.AcceptTurn(ctx, id, "t1", base, nil, "ai: work")
	if err != nil || conflicts != nil || commit == "" {
		t.Fatalf("accept: %q %v %v", commit, conflicts, err)
	}
	if !strings.Contains(read(t, s.Path(id), "intro.tex"), "line 2 (AI)") || read(t, s.Path(id), "notes.tex") != "new\n" {
		t.Fatal("changes not applied")
	}
	if got := commitCount(t, s, id); got != before+1 {
		t.Fatalf("commits = %d, want exactly one new commit", got-before)
	}
	if out, _ := git(ctx, s.Path(id), "status", "--porcelain"); out != "" {
		t.Fatalf("working copy dirty after accept: %q", out)
	}
	if err := s.EndTurn(ctx, id, "t1"); err != nil {
		t.Fatal(err)
	}
}

func TestTurnAcceptPerFile(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	base := aiTurn(t, s, id, "t1")
	if _, c, err := s.AcceptTurn(ctx, id, "t1", base, []string{"notes.tex"}, "ai: notes"); err != nil || c != nil {
		t.Fatal(c, err)
	}
	if read(t, s.Path(id), "notes.tex") != "new\n" || read(t, s.Path(id), "intro.tex") != intro {
		t.Fatal("per-file accept applied the wrong files")
	}
	// The rest can be accepted later.
	if _, c, err := s.AcceptTurn(ctx, id, "t1", base, []string{"intro.tex"}, "ai: intro"); err != nil || c != nil {
		t.Fatal(c, err)
	}
	if !strings.Contains(read(t, s.Path(id), "intro.tex"), "(AI)") {
		t.Fatal("second accept not applied")
	}
}

func TestTurnAcceptMergesUserEdits(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	base := aiTurn(t, s, id, "t1")
	// While the turn ran, the user edited another line of the same file (not committed).
	s.WriteFile(ctx, id, "intro.tex", strings.NewReader(strings.Replace(intro, "line 5", "line 5 (user)", 1)))
	if _, c, err := s.AcceptTurn(ctx, id, "t1", base, nil, "ai: work"); err != nil || c != nil {
		t.Fatal(c, err)
	}
	got := read(t, s.Path(id), "intro.tex")
	if !strings.Contains(got, "line 2 (AI)") || !strings.Contains(got, "line 5 (user)") {
		t.Fatalf("3-way merge lost a side:\n%s", got)
	}
}

func TestTurnAcceptConflictChangesNothing(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	base := aiTurn(t, s, id, "t1")
	userVersion := strings.Replace(intro, "line 2", "line 2 (user)", 1)
	s.WriteFile(ctx, id, "intro.tex", strings.NewReader(userVersion))
	before := commitCount(t, s, id)
	commit, conflicts, err := s.AcceptTurn(ctx, id, "t1", base, nil, "ai: work")
	if err != nil || commit != "" || len(conflicts) != 1 || conflicts[0] != "intro.tex" {
		t.Fatalf("accept = %q %v %v, want a conflict on intro.tex", commit, conflicts, err)
	}
	if read(t, s.Path(id), "intro.tex") != userVersion {
		t.Fatal("conflict touched the user's file")
	}
	if _, err := os.Stat(filepath.Join(s.Path(id), "notes.tex")); err == nil {
		t.Fatal("conflict partially applied the turn")
	}
	// Only the checkpoint of the user's own edit was committed.
	if got := commitCount(t, s, id); got != before+1 {
		t.Fatalf("commits = +%d", got-before)
	}
}

func TestTurnReject(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	before := commitCount(t, s, id)
	aiTurn(t, s, id, "t1")
	if err := s.EndTurn(ctx, id, "t1"); err != nil {
		t.Fatal(err)
	}
	if got := commitCount(t, s, id); got != before {
		t.Fatalf("history changed: +%d", got-before)
	}
	if _, err := os.Stat(s.worktreeDir(id, "t1")); !os.IsNotExist(err) {
		t.Fatal("worktree not removed")
	}
	if out, _ := git(ctx, s.Path(id), "branch", "--list", "ai/*"); out != "" {
		t.Fatalf("branch left: %q", out)
	}
	if read(t, s.Path(id), "intro.tex") != intro {
		t.Fatal("reject changed the project")
	}
	// Worktrees never show up in the project tree or exports.
	files, _ := s.ListFiles(ctx, id)
	for _, f := range files {
		if strings.Contains(f.Path, "worktrees") {
			t.Fatalf("worktree visible: %s", f.Path)
		}
	}
}
