package projects

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// git runs the git CLI in dir, isolated from any system/global config and
// with hooks disabled: a project's content must never make git execute code.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	return gitEnv(ctx, dir, nil, args...)
}

// gitEnv is git with extra environment entries (secrets go here, not in args).
func gitEnv(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	base := []string{
		"-c", "core.hooksPath=/dev/null",
		"-c", "user.name=Refract",
		"-c", "user.email=refract@localhost",
		"-c", "commit.gpgsign=false",
		"-c", "init.defaultBranch=main",
	}
	// Never killed half-way: an interrupted `git add`/`commit` leaves
	// .git/index.lock behind and blocks the repo. Git ops are local and short;
	// the timeout only guards against a hang.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append(base, args...)...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, env...)
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %v: %w: %s", args, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return out.String(), nil
}

// initRepo makes dir a git repo with one commit of its current content.
// Build output (.refract/) is excluded via .git/info/exclude so no file is
// added to the user's project.
func initRepo(ctx context.Context, dir, message string) error {
	if _, err := git(ctx, dir, "init", "-q"); err != nil {
		return err
	}
	info := filepath.Join(dir, ".git", "info")
	if err := os.MkdirAll(info, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(info, "exclude"), []byte(".refract/\n"), 0o644); err != nil {
		return err
	}
	if _, err := git(ctx, dir, "add", "-A"); err != nil {
		return err
	}
	_, err := git(ctx, dir, "commit", "-q", "--allow-empty", "-m", message)
	return err
}

// Checkpoint commits the working tree if anything changed, and reports whether it did.
func (s *Service) Checkpoint(ctx context.Context, id, message string) (bool, error) {
	return s.checkpoint(ctx, id, message, true)
}

// committed tells OnCommit (auto-push) that the project's history grew. It
// gets the context of whoever committed, so it acts as them: their user, not
// their request's deadline.
func (s *Service) committed(ctx context.Context, id string) {
	if s.OnCommit != nil {
		s.bg.Add(1)
		go func() {
			defer s.bg.Done()
			s.OnCommit(context.WithoutCancel(ctx), id)
		}()
	}
}

// Wait waits up to timeout for the OnCommit calls still running, and reports
// whether they all finished. Shutdown calls it before closing the database,
// which they read.
func (s *Service) Wait(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		s.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

func (s *Service) checkpoint(ctx context.Context, id, message string, notify bool) (bool, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return false, err
	}
	dir := s.Path(id)
	if _, err := git(ctx, dir, "add", "-A"); err != nil {
		return false, err
	}
	if out, err := git(ctx, dir, "status", "--porcelain"); err != nil || out == "" {
		return false, err
	}
	if _, err := git(ctx, dir, "commit", "-q", "-m", message); err != nil {
		return false, err
	}
	if notify {
		s.committed(ctx, id)
	}
	return true, nil
}
