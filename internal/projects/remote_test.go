package projects

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
)

// remoteProject is a project plus a bare repository acting as its remote, and
// a second clone ("elsewhere") standing for another computer.
func remoteProject(t *testing.T) (s *Service, id, bare string) {
	t.Helper()
	allowFileRemotes = true
	t.Cleanup(func() { allowFileRemotes = false })
	s, id = newTurnProject(t)
	s.Key = make([]byte, 32)
	bare = filepath.Join(t.TempDir(), "remote.git")
	if _, err := git(ownerCtx, t.TempDir(), "init", "-q", "--bare", bare); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRemote(ownerCtx, id, Remote{URL: "file://" + bare, Token: "secret"}, true); err != nil {
		t.Fatal(err)
	}
	return s, id, bare
}

// elsewhere commits a file in another clone of the remote and pushes it.
func elsewhere(t *testing.T, bare, rel, body string) {
	t.Helper()
	ctx := ownerCtx
	dir := filepath.Join(t.TempDir(), "clone")
	allow := []string{"-c", "protocol.file.allow=always"}
	if _, err := git(ctx, t.TempDir(), append(allow, "clone", "-q", "file://"+bare, dir)...); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644)
	git(ctx, dir, "add", "-A")
	if _, err := git(ctx, dir, "commit", "-q", "-m", "edit elsewhere"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, dir, append(allow, "push", "-q", "origin", "HEAD:main")...); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteSettings(t *testing.T) {
	ctx := ownerCtx
	s, id := newTurnProject(t)
	s.Key = make([]byte, 32)
	if _, err := s.Remote(ctx, id); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("no remote: %v", err)
	}
	for _, bad := range []Remote{
		{URL: "http://github.com/a/b.git"},
		{URL: "git@github.com:a/b.git"},
		{URL: "ssh://github.com/a/b.git"},
		{URL: "https://user:pass@github.com/a/b.git"},
		{URL: "file:///etc"},
		{URL: "ext::sh -c touch% /tmp/pwned"},
		{URL: "https://github.com/a/b.git", Branch: "../x"},
	} {
		if err := s.SetRemote(ctx, id, bad, true); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
	if err := s.SetRemote(ctx, id, Remote{URL: "https://github.com/a/b.git", Token: "tok.secret.1", AutoPush: true}, true); err != nil {
		t.Fatal(err)
	}
	// An empty token keeps the stored one; the branch defaults to main.
	s.SetRemote(ctx, id, Remote{URL: "https://github.com/a/c.git"}, true)
	r, err := s.Remote(ctx, id)
	if err != nil || r.Token != "tok.secret.1" || r.Branch != "main" || r.URL != "https://github.com/a/c.git" {
		t.Fatalf("remote = %+v, %v", r, err)
	}
	// Sealed at rest. The token contains dots, which base64 never does, so a match can only mean it is stored in clear.
	var raw string
	s.DB.QueryRow(`SELECT value FROM settings WHERE key = ?`, remoteKey(id)).Scan(&raw)
	if strings.Contains(raw, "tok.secret.1") || strings.Contains(raw, "github.com") {
		t.Errorf("stored in clear: %q", raw)
	}
	if err := s.SetRemote(ctx, id, Remote{}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remote(ctx, id); !errors.Is(err, ErrNoRemote) {
		t.Errorf("not removed: %v", err)
	}
}

func TestPushPull(t *testing.T) {
	ctx := ownerCtx
	s, id, bare := remoteProject(t)

	// An empty remote: nothing to pull; then push.
	if res, err := s.Pull(ctx, id); err != nil || !res.Empty {
		t.Fatalf("pull empty = %+v, %v", res, err)
	}
	os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("pending edit\n"), 0o644)
	if err := s.Push(ctx, id); err != nil {
		t.Fatal(err)
	}
	if out, _ := git(ctx, bare, "show", "main:main.tex"); out != "pending edit\n" {
		t.Errorf("remote main.tex = %q (pending edits are committed before pushing)", out)
	}

	// A change made elsewhere comes in.
	elsewhere(t, bare, "intro.tex", "from elsewhere\n")
	res, err := s.Pull(ctx, id)
	if err != nil || len(res.Changed) != 1 || res.Changed[0] != "intro.tex" || read(t, s.Path(id), "intro.tex") != "from elsewhere\n" {
		t.Fatalf("pull = %+v, %v", res, err)
	}

	// Both sides changed different files: merged.
	elsewhere(t, bare, "intro.tex", "again elsewhere\n")
	os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("local edit\n"), 0o644)
	if err := s.Push(ctx, id); !errors.Is(err, ErrBehind) {
		t.Fatalf("push while behind = %v", err)
	}
	res, err = s.Pull(ctx, id)
	if err != nil || len(res.Conflicts) != 0 || read(t, s.Path(id), "intro.tex") != "again elsewhere\n" || read(t, s.Path(id), "main.tex") != "local edit\n" {
		t.Fatalf("merge = %+v, %v", res, err)
	}
	if err := s.Push(ctx, id); err != nil {
		t.Fatalf("push after merge: %v", err)
	}

	// Both sides changed the same lines: nothing is merged.
	elsewhere(t, bare, "main.tex", "theirs\n")
	os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("ours\n"), 0o644)
	res, err = s.Pull(ctx, id)
	if err != nil || len(res.Conflicts) != 1 || res.Conflicts[0] != "main.tex" || read(t, s.Path(id), "main.tex") != "ours\n" {
		t.Fatalf("conflict = %+v, %v", res, err)
	}
	if out, _ := git(ctx, s.Path(id), "status", "--porcelain"); out != "" {
		t.Errorf("left a merge in progress: %q", out)
	}
}

func TestOnCommitFires(t *testing.T) {
	s, id := newTurnProject(t)
	fired := make(chan string, 4)
	s.OnCommit = func(ctx context.Context, id string) {
		if u, _ := auth.UserFrom(ctx); u.ID != "owner" {
			t.Errorf("OnCommit runs as %q, want the committing user", u.ID)
		}
		fired <- id
	}
	os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("x\n"), 0o644)
	if _, err := s.Checkpoint(ownerCtx, id, "c"); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-fired:
		if got != id {
			t.Errorf("fired for %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnCommit not called")
	}
}

// Shutdown waits for auto-pushes in progress before closing the database.
func TestDrainWaitsForOnCommit(t *testing.T) {
	s, id := newTurnProject(t)
	started, release := make(chan struct{}), make(chan struct{})
	s.OnCommit = func(ctx context.Context, id string) {
		close(started)
		<-release
	}
	os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("x\n"), 0o644)
	if _, err := s.Checkpoint(ownerCtx, id, "c"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("OnCommit not called")
	}
	if s.Drain(50 * time.Millisecond) {
		t.Fatal("Drain returned true while OnCommit was running")
	}
	close(release)
	if !s.Drain(2 * time.Second) {
		t.Fatal("Drain timed out after OnCommit finished")
	}
}

// Remote operations act with the acting user's credentials, so they only run
// on that user's own project: the token is never someone else's.
func TestRemoteActsAsItsUser(t *testing.T) {
	s := newService(t)
	s.Key = make([]byte, 32)
	p, err := s.Create(ownerCtx, "paper")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRemote(ownerCtx, p.ID, Remote{URL: "https://127.0.0.1:1/paper.git", Branch: "main", GitHub: true}, false); err != nil {
		t.Fatal(err)
	}
	var asked []string
	s.GitHubToken = func(_ context.Context, user string) (string, error) {
		asked = append(asked, user)
		return "token-of-" + user, nil
	}
	r, err := s.Remote(ownerCtx, p.ID)
	if err != nil || r.Token != "token-of-owner" {
		t.Fatalf("owner: %+v, %v", r, err)
	}

	// The project now belongs to someone else (no such feature yet; anything
	// that adds one must hold the remote lock): the old owner's push stops
	// before resolving credentials or committing.
	s.DB.Exec(`UPDATE projects SET owner = 'ben' WHERE id = ?`, p.ID)
	s.WriteFile(ownerCtx, p.ID, "late.tex", strings.NewReader("x"))
	before, _ := s.History(ownerCtx, p.ID, 100)
	asked = nil
	if err := s.Push(ownerCtx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("push by the former owner: %v", err)
	}
	if _, err := s.Pull(ownerCtx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("pull by the former owner: %v", err)
	}
	if _, err := s.Remote(context.Background(), p.ID); err == nil {
		t.Error("remote without a user")
	}
	after, _ := s.History(ownerCtx, p.ID, 100)
	if len(asked) != 0 || len(after) != len(before) {
		t.Errorf("tokens asked %v, commits %d → %d", asked, len(before), len(after))
	}
}
