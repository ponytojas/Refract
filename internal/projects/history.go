package projects

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Commit is one entry of a project's history.
type Commit struct {
	Hash    string    `json:"hash"`
	Short   string    `json:"short"`
	Message string    `json:"message"`
	Time    time.Time `json:"time"`
}

// FileDiff is a file's content before and after a change, for diff views.
type FileDiff struct {
	Change
	Before string `json:"before"`
	After  string `json:"after"`
	Binary bool   `json:"binary"`
}

// NewFileDiff fills Before/After, or marks the file binary.
func NewFileDiff(c Change, before, after []byte) FileDiff {
	d := FileDiff{Change: c}
	if utf8.Valid(before) && utf8.Valid(after) && !slices.Contains(before, 0) && !slices.Contains(after, 0) {
		d.Before, d.After = string(before), string(after)
	} else {
		d.Binary = true
	}
	return d
}

// ErrBadRevision is returned for anything that is not a commit hash (optionally with ^ for its parent).
var ErrBadRevision = errors.New("not a commit")

var revision = regexp.MustCompile(`^[0-9a-f]{7,40}\^?$`)

// The empty tree: the "before" of a project's first commit.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// History lists the newest commits first.
func (s *Service) History(ctx context.Context, id string, limit int) ([]Commit, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	out, err := git(ctx, s.Path(id), "log", "-n", strconv.Itoa(limit), "--format=%H%x1f%h%x1f%at%x1f%s%x1e", "HEAD")
	if err != nil {
		return nil, err
	}
	commits := []Commit{}
	for _, rec := range strings.Split(strings.TrimSpace(out), "\x1e") {
		f := strings.Split(strings.TrimSpace(rec), "\x1f")
		if len(f) != 4 {
			continue
		}
		at, _ := strconv.ParseInt(f[2], 10, 64)
		commits = append(commits, Commit{Hash: f[0], Short: f[1], Message: f[3], Time: time.Unix(at, 0)})
	}
	return commits, nil
}

// CommitDiff returns what a commit changed, file by file, against its first parent.
func (s *Service) CommitDiff(ctx context.Context, id, hash string) ([]FileDiff, error) {
	if !revision.MatchString(hash) || strings.HasSuffix(hash, "^") {
		return nil, ErrBadRevision
	}
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	dir := s.Path(id)
	parent := hash + "^"
	if _, err := git(ctx, dir, "rev-parse", "--verify", "-q", parent); err != nil {
		parent = emptyTree
	}
	changes, err := diffNames(ctx, dir, parent, hash)
	if err != nil {
		return nil, err
	}
	out := []FileDiff{}
	for _, c := range changes {
		out = append(out, NewFileDiff(c, show(ctx, dir, parent, c.Path), show(ctx, dir, hash, c.Path)))
	}
	return out, nil
}

// RestoreFile brings path back as it was at rev (a commit, or commit^ for the
// version before it) and commits that. Pending edits are checkpointed first,
// so nothing is lost and history is never rewritten. A path absent at rev is
// deleted. It returns the new commit, or "" when nothing changed.
func (s *Service) RestoreFile(ctx context.Context, id, rev, path string) (string, error) {
	if !revision.MatchString(rev) {
		return "", ErrBadRevision
	}
	path, err := checkPath(path)
	if err != nil {
		return "", err
	}
	defer lockRepo(id)()
	if _, err := s.checkpoint(ctx, id, "Checkpoint before restore", true); err != nil {
		return "", err
	}
	dir := s.Path(id)
	if _, err := git(ctx, dir, "cat-file", "-e", rev+":"+path); err == nil {
		_, err = git(ctx, dir, "checkout", rev, "--", path)
		if err != nil {
			return "", err
		}
	} else if _, err := git(ctx, dir, "rm", "-q", "--ignore-unmatch", "--", path); err != nil {
		return "", err
	}
	return s.commitIfChanged(ctx, id, "Restore "+path+" from "+short(rev))
}

// RestoreProject makes every tracked file match commit hash (files added
// since are removed) and commits that as a new commit.
func (s *Service) RestoreProject(ctx context.Context, id, hash string) (string, error) {
	if !revision.MatchString(hash) || strings.HasSuffix(hash, "^") {
		return "", ErrBadRevision
	}
	defer lockRepo(id)()
	if _, err := s.checkpoint(ctx, id, "Checkpoint before restore", true); err != nil {
		return "", err
	}
	if _, err := git(ctx, s.Path(id), "restore", "--source="+hash, "--staged", "--worktree", "--", "."); err != nil {
		return "", err
	}
	return s.commitIfChanged(ctx, id, "Restore the project to "+short(hash))
}

func (s *Service) commitIfChanged(ctx context.Context, id, message string) (string, error) {
	dir := s.Path(id)
	if out, err := git(ctx, dir, "status", "--porcelain"); err != nil || out == "" {
		return "", err
	}
	if _, err := git(ctx, dir, "commit", "-q", "-m", message); err != nil {
		return "", err
	}
	if err := s.touch(ctx, id); err != nil {
		return "", err
	}
	s.committed(ctx, id)
	out, err := git(ctx, dir, "rev-parse", "HEAD")
	return strings.TrimSpace(out), err
}

// short names a revision for commit messages: "abc1234", or "before abc1234" for its parent.
func short(rev string) string {
	hash, parent := strings.CutSuffix(rev, "^")
	hash = hash[:min(7, len(hash))]
	if parent {
		return "before " + hash
	}
	return hash
}

// diffNames lists files that differ between two revisions.
func diffNames(ctx context.Context, dir, a, b string) ([]Change, error) {
	out, err := git(ctx, dir, "diff", "--name-status", "--no-renames", "-z", a, b)
	if err != nil {
		return nil, err
	}
	changes := []Change{}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		st := map[string]string{"A": "added", "M": "modified", "D": "deleted"}[fields[i][:1]]
		if st == "" {
			st = "modified"
		}
		changes = append(changes, Change{Path: fields[i+1], Status: st})
	}
	return changes, nil
}

// show returns a file's content at rev, or nil when absent.
func show(ctx context.Context, dir, rev, path string) []byte {
	out, err := git(ctx, dir, "show", rev+":"+path)
	if err != nil {
		return nil
	}
	return []byte(out)
}
