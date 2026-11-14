package projects

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// ErrBadPath rejects a file path from the API (outside the project, or git/build internals).
var ErrBadPath = errors.New("invalid path")

// MaxFileSize caps a single write (editor save or asset upload).
const MaxFileSize = 50 << 20

// FileEntry is one item of the project tree.
type FileEntry struct {
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"`
}

// checkPath validates an API path. os.Root already confines access to the
// project; this also keeps .git (whose config git would execute) and .refract
// out of reach, which os.Root alone would allow.
func checkPath(p string) (string, error) {
	clean, skip, _, err := cleanName(p)
	if err != nil || skip || isJunk(p) {
		return "", fmt.Errorf("%w: %q", ErrBadPath, p)
	}
	return clean, nil
}

// root opens a project's directory for confined access, after checking the id exists.
func (s *Service) root(ctx context.Context, id string) (*os.Root, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	return os.OpenRoot(s.Path(id))
}

// ListFiles returns every file and folder (sorted), excluding .git and .refract.
func (s *Service) ListFiles(ctx context.Context, id string) ([]FileEntry, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	dir := s.Path(id)
	out := []FileEntry{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".refract") {
			return filepath.SkipDir
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		e := FileEntry{Path: filepath.ToSlash(rel), Dir: d.IsDir(), MTime: info.ModTime().Unix()}
		if !e.Dir {
			e.Size = info.Size()
		}
		out = append(out, e)
		return nil
	})
	slices.SortFunc(out, func(a, b FileEntry) int { return strings.Compare(a.Path, b.Path) })
	return out, err
}

// ReadFile returns a file's bytes and whether it looks like text (UTF-8, no NUL).
func (s *Service) ReadFile(ctx context.Context, id, p string) ([]byte, bool, error) {
	p, err := checkPath(p)
	if err != nil {
		return nil, false, err
	}
	root, err := s.root(ctx, id)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	b, err := root.ReadFile(p)
	if err != nil {
		return nil, false, err
	}
	head := b[:min(len(b), 8192)]
	text := !slices.Contains(head, 0) && utf8.Valid(trimPartialRune(head))
	return b, text, nil
}

// trimPartialRune drops a rune cut in half at the end of a prefix.
func trimPartialRune(b []byte) []byte {
	for i := 0; i < utf8.UTFMax && len(b) > 0; i++ {
		if utf8.Valid(b) {
			return b
		}
		b = b[:len(b)-1]
	}
	return b
}

// WriteFile creates or replaces a file atomically (temp file + rename), creating parent folders.
func (s *Service) WriteFile(ctx context.Context, id, p string, r io.Reader) error {
	p, err := checkPath(p)
	if err != nil {
		return err
	}
	root, err := s.root(ctx, id)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := s.checkQuotaFor(ctx, id); err != nil {
		return err
	}
	if d := path.Dir(p); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if fi, err := root.Stat(p); err == nil && fi.IsDir() {
		return fmt.Errorf("%w: %q is a folder", ErrBadPath, p)
	}
	suffix := make([]byte, 6)
	rand.Read(suffix)
	tmp := path.Join(path.Dir(p), ".refract-tmp-"+hex.EncodeToString(suffix))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	defer root.Remove(tmp) // no-op after the rename
	n, err := io.Copy(f, io.LimitReader(r, MaxFileSize+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if n > MaxFileSize {
		return fmt.Errorf("%w: file larger than %d MB", ErrTooLarge, MaxFileSize>>20)
	}
	if err := root.Rename(tmp, p); err != nil {
		return err
	}
	s.grew(ctx, id, n)
	return s.touch(ctx, id)
}

// Mkdir creates a folder (and parents).
func (s *Service) Mkdir(ctx context.Context, id, p string) error {
	p, err := checkPath(p)
	if err != nil {
		return err
	}
	root, err := s.root(ctx, id)
	if err != nil {
		return err
	}
	defer root.Close()
	return root.MkdirAll(p, 0o755)
}

// Move renames a file or folder; it never overwrites an existing target.
func (s *Service) Move(ctx context.Context, id, from, to string) error {
	from, err := checkPath(from)
	if err != nil {
		return err
	}
	if to, err = checkPath(to); err != nil {
		return err
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return fmt.Errorf("%w: cannot move %q into itself", ErrBadPath, from)
	}
	root, err := s.root(ctx, id)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(to); err == nil {
		return fmt.Errorf("%w: %q already exists", fs.ErrExist, to)
	}
	if d := path.Dir(to); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := root.Rename(from, to); err != nil {
		return err
	}
	return s.touch(ctx, id)
}

// Remove deletes a file or a folder with its content.
func (s *Service) Remove(ctx context.Context, id, p string) error {
	p, err := checkPath(p)
	if err != nil {
		return err
	}
	root, err := s.root(ctx, id)
	if err != nil {
		return err
	}
	defer root.Close()
	if _, err := root.Lstat(p); err != nil {
		return err
	}
	if err := root.RemoveAll(p); err != nil {
		return err
	}
	return s.touch(ctx, id)
}

func (s *Service) touch(ctx context.Context, id string) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE projects SET updated_at = ? WHERE id = ?`, time.Now().Unix(), id)
	return err
}
