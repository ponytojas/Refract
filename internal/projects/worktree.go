package projects

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Each AI turn works in its own git worktree on branch ai/<turn>, created
// from HEAD. The user's working copy is untouched until they accept.

// Change is one file changed by a turn.
type Change struct {
	Path   string `json:"path"`
	Status string `json:"status"` // added | modified | deleted
}

func (s *Service) worktreeDir(id, name string) string {
	return filepath.Join(s.Path(id), ".refract", "worktrees", name)
}

func branch(turn string) string { return "ai/" + turn }

// StartTurn commits any unsaved-to-git edits (so the AI sees what the user
// sees), then creates the turn's worktree. It returns the base commit and the
// worktree directory.
func (s *Service) StartTurn(ctx context.Context, id, turn string) (base, dir string, err error) {
	defer lockRepo(id)()
	if err := s.checkQuotaFor(ctx, id); err != nil {
		return "", "", err
	}
	if _, err := s.checkpoint(ctx, id, "checkpoint: before AI", true); err != nil {
		return "", "", err
	}
	root := s.Path(id)
	out, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", "", err
	}
	base = strings.TrimSpace(out)
	dir = s.worktreeDir(id, turn)
	if _, err := git(ctx, root, "worktree", "add", "-q", "-b", branch(turn), dir, base); err != nil {
		return "", "", err
	}
	s.measure(ctx, id)
	return base, dir, nil
}

// FinishTurn commits the worktree's changes on the turn's branch and lists
// what changed relative to base.
func (s *Service) FinishTurn(ctx context.Context, id, turn, base, message string) ([]Change, error) {
	defer lockRepo(id)()
	dir := s.worktreeDir(id, turn)
	if _, err := git(ctx, dir, "add", "-A"); err != nil {
		return nil, err
	}
	if out, err := git(ctx, dir, "status", "--porcelain"); err != nil {
		return nil, err
	} else if out != "" {
		if _, err := git(ctx, dir, "commit", "-q", "-m", message); err != nil {
			return nil, err
		}
	}
	return s.TurnChanges(ctx, id, turn, base)
}

// TurnChanges lists files that differ between base and the turn's branch.
func (s *Service) TurnChanges(ctx context.Context, id, turn, base string) ([]Change, error) {
	return diffNames(ctx, s.Path(id), base, branch(turn))
}

// TurnFile returns a file's content at base and on the turn's branch (nil when absent).
func (s *Service) TurnFile(ctx context.Context, id, turn, base, path string) (before, after []byte, err error) {
	if path, err = checkPath(path); err != nil {
		return nil, nil, err
	}
	return show(ctx, s.Path(id), base, path), show(ctx, s.Path(id), branch(turn), path), nil
}

// ErrConflict means accepting would conflict with changes made since the turn started.
var ErrConflict = errors.New("conflict")

// AcceptTurn applies the turn's changes to files (all pending files if empty)
// onto the current HEAD with a 3-way merge, and commits them. The merge runs
// in a temporary worktree: on conflict nothing touches the user's copy and the
// conflicting files are returned.
func (s *Service) AcceptTurn(ctx context.Context, id, turn, base string, files []string, message string) (commit string, conflicts []string, err error) {
	for i, f := range files {
		if files[i], err = checkPath(f); err != nil {
			return "", nil, err
		}
	}
	defer lockRepo(id)()
	root := s.Path(id)
	// Edits made during the turn become part of HEAD, so the merge sees them.
	if _, err := s.checkpoint(ctx, id, "checkpoint: before accepting AI changes", true); err != nil {
		return "", nil, err
	}
	patch, err := git(ctx, root, append([]string{"diff", "--binary", "--no-renames", base, branch(turn), "--"}, files...)...)
	if err != nil {
		return "", nil, err
	}
	if patch == "" {
		return "", nil, nil
	}

	tmp := s.worktreeDir(id, "accept-"+turn)
	os.RemoveAll(tmp)
	git(ctx, root, "worktree", "prune")
	if _, err := git(ctx, root, "worktree", "add", "-q", "--detach", tmp, "HEAD"); err != nil {
		return "", nil, err
	}
	defer func() {
		git(context.WithoutCancel(ctx), root, "worktree", "remove", "--force", tmp)
	}()
	patchFile := filepath.Join(tmp, ".refract-turn.patch")
	if err := os.WriteFile(patchFile, []byte(patch), 0o600); err != nil {
		return "", nil, err
	}
	if _, err := git(ctx, tmp, "apply", "--3way", "--whitespace=nowarn", patchFile); err != nil {
		os.Remove(patchFile)
		out, _ := git(ctx, tmp, "diff", "--name-only", "--diff-filter=U")
		conflicts = strings.Fields(out)
		if len(conflicts) == 0 { // the patch did not apply at all (e.g. the file was deleted)
			conflicts = files
			if len(conflicts) == 0 {
				changes, _ := s.TurnChanges(ctx, id, turn, base)
				for _, c := range changes {
					conflicts = append(conflicts, c.Path)
				}
			}
		}
		return "", conflicts, nil
	}
	os.Remove(patchFile)
	if _, err := git(ctx, tmp, "commit", "-q", "-m", message); err != nil {
		return "", nil, err
	}
	out, err := git(ctx, tmp, "rev-parse", "HEAD")
	if err != nil {
		return "", nil, err
	}
	commit = strings.TrimSpace(out)
	// Fast-forward the user's copy. Refuses (and changes nothing) if the user
	// edited one of these files in the last instant; they can just retry.
	if _, err := git(ctx, root, "merge", "-q", "--ff-only", commit); err != nil {
		return "", nil, fmt.Errorf("%w: files changed while accepting, try again: %v", ErrConflict, err)
	}
	s.committed(ctx, id)
	return commit, nil, s.touch(ctx, id)
}

// EndTurn removes the turn's worktree and branch (after reject, or once everything was accepted).
func (s *Service) EndTurn(ctx context.Context, id, turn string) error {
	defer lockRepo(id)()
	defer s.measure(ctx, id)
	root := s.Path(id)
	git(ctx, root, "worktree", "remove", "--force", s.worktreeDir(id, turn))
	os.RemoveAll(s.worktreeDir(id, turn))
	git(ctx, root, "worktree", "prune")
	if _, err := git(ctx, root, "rev-parse", "--verify", "-q", branch(turn)); err != nil {
		return nil // already gone
	}
	_, err := git(ctx, root, "branch", "-D", branch(turn))
	return err
}

// CheckPath validates a project-relative path the way every file API does
// (used by the AI tools on a turn's worktree).
func CheckPath(p string) (string, error) { return checkPath(p) }
