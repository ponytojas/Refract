// Package projects stores LaTeX projects: one git repository per project
// under <data>/projects/<id>, metadata in SQLite.
package projects

import (
	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/store"

	"archive/zip"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned for an unknown project id.
var ErrNotFound = errors.New("project not found")

// Project is a project's metadata.
type Project struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	MainFile  string    `json:"main_file"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Owner     string    `json:"-"` // user id
}

// ImportResult is what the UI shows after an import.
type ImportResult struct {
	Project    *Project  `json:"project"`
	Warnings   []Warning `json:"warnings"`
	Skipped    []string  `json:"skipped,omitempty"`    // entries dropped (e.g. .git/)
	Candidates []string  `json:"candidates,omitempty"` // .tex files, set when the main file is unknown
}

// Service manages projects.
type Service struct {
	DB       *sql.DB
	Dir      string // <data>/projects
	Limits   Limits
	Key      []byte                               // seals remote tokens
	OnCommit func(ctx context.Context, id string) // called (in its own goroutine, as the committing user) after commits on the main branch
	// GitHubToken returns the GitHub token of a user (a project's owner), for remotes that use their account.
	GitHubToken func(ctx context.Context, owner string) (string, error)

	// PublicRemotes (the hosted service): git remotes must resolve to public
	// addresses only, and git connects to the address that was checked.
	PublicRemotes bool

	bgMu     sync.Mutex     // guards stopping, and orders bg.Add before bg.Wait
	stopping bool           // Drain started: no new OnCommit calls
	bg       sync.WaitGroup // OnCommit calls still running
}

// Path returns a project's directory.
func (s *Service) Path(id string) string { return filepath.Join(s.Dir, id) }

const projectCols = `id, name, main_file, created_at, updated_at, owner`

// List returns the request user's projects; none without a user.
func (s *Service) List(ctx context.Context) ([]Project, error) {
	u, err := access.UserFrom(ctx)
	if err != nil {
		return []Project{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+projectCols+` FROM projects WHERE owner = ? ORDER BY updated_at DESC`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Project{}
	for rows.Next() {
		p, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// Get returns a project, whoever owns it. Who may reach it is decided before:
// every API route that addresses a project goes through access.Require (the
// route table in cmd/refract), and background work rechecks before writing.
// Code calling the service directly must do the same.
func (s *Service) Get(ctx context.Context, id string) (*Project, error) {
	p, err := scan(s.DB.QueryRowContext(ctx, `SELECT `+projectCols+` FROM projects WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// Create makes an empty project.
func (s *Service) Create(ctx context.Context, name string) (*Project, error) {
	res, err := s.create(ctx, name, func(string) ([]string, error) { return nil, nil }, "create project")
	if err != nil {
		return nil, err
	}
	return res.Project, nil
}

// Import creates a project from src; the import becomes the initial commit.
func (s *Service) Import(ctx context.Context, name string, src Source) (*ImportResult, error) {
	return s.create(ctx, name, func(dir string) ([]string, error) { return extract(dir, src, s.Limits) }, "import")
}

// ImportZip imports a zip file from disk.
func (s *Service) ImportZip(ctx context.Context, name, zipPath string) (*ImportResult, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return s.Import(ctx, name, ZipSource(&zr.Reader))
}

// create builds the project in a temp dir and only moves it into place and
// registers it once everything succeeded, so a failed import leaves nothing.
func (s *Service) create(ctx context.Context, name string, fill func(dir string) ([]string, error), message string) (*ImportResult, error) {
	u, err := access.UserFrom(ctx) // a project nobody owns would be unreachable
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = "Untitled"
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return nil, err
	}
	tmp, err := os.MkdirTemp(s.Dir, ".tmp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp) // no-op after the rename

	skipped, err := fill(tmp)
	if err != nil {
		return nil, err
	}
	main, candidates, err := detectMain(tmp)
	if err != nil {
		return nil, err
	}
	warnings, err := Check(tmp)
	if err != nil {
		return nil, err
	}
	if err := initRepo(ctx, tmp, message); err != nil {
		return nil, err
	}

	id := newID()
	if err := os.Rename(tmp, s.Path(id)); err != nil {
		return nil, err
	}
	now := time.Now()
	p := &Project{ID: id, Name: name, MainFile: main, CreatedAt: now, UpdatedAt: now, Owner: u.ID}
	if _, err := s.DB.ExecContext(ctx, `INSERT INTO projects (id, name, main_file, created_at, updated_at, owner) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.MainFile, now.Unix(), now.Unix(), p.Owner); err != nil {
		os.RemoveAll(s.Path(id))
		return nil, err
	}
	res := &ImportResult{Project: p, Warnings: warnings, Skipped: skipped}
	if main == "" {
		res.Candidates = candidates
	}
	if res.Warnings == nil {
		res.Warnings = []Warning{}
	}
	return res, nil
}

func (s *Service) Rename(ctx context.Context, id, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name required")
	}
	return s.update(ctx, id, `UPDATE projects SET name = ?, updated_at = ? WHERE id = ?`, name)
}

// SetMainFile sets the file to compile; it must be an existing .tex file in the project.
func (s *Service) SetMainFile(ctx context.Context, id, rel string) error {
	if _, err := s.Get(ctx, id); err != nil { // only ids from the DB ever reach Path
		return err
	}
	root, err := os.OpenRoot(s.Path(id))
	if err != nil {
		return err
	}
	defer root.Close()
	if !strings.HasSuffix(strings.ToLower(rel), ".tex") {
		return errors.New("main file must be a .tex file")
	}
	if fi, err := root.Stat(rel); err != nil || !fi.Mode().IsRegular() {
		return errors.New("main file not found in the project")
	}
	return s.update(ctx, id, `UPDATE projects SET main_file = ?, updated_at = ? WHERE id = ?`, filepath.ToSlash(filepath.Clean(rel)))
}

func (s *Service) update(ctx context.Context, id, query, value string) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	res, err := s.DB.ExecContext(ctx, query, value, time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes the project and its history.
func (s *Service) Delete(ctx context.Context, id string) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id); err != nil {
		return err
	}
	store.SetSetting(ctx, s.DB, remoteKey(id), "") // its remote and token
	return os.RemoveAll(s.Path(id))
}

// Check re-runs the portability checks on a project.
func (s *Service) Check(ctx context.Context, id string) ([]Warning, error) {
	if _, err := s.Get(ctx, id); err != nil {
		return nil, err
	}
	w, err := Check(s.Path(id))
	if w == nil {
		w = []Warning{}
	}
	return w, err
}

// Export writes the working tree (without .git and .refract) as a zip.
func (s *Service) Export(ctx context.Context, id string, w io.Writer) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	dir := s.Path(id)
	var files []string
	if err := walkFiles(dir, func(rel string, _ fs.FileInfo) error {
		files = append(files, rel)
		return nil
	}); err != nil {
		return err
	}
	slices.Sort(files) // deterministic archive
	zw := zip.NewWriter(w)
	for _, rel := range files {
		if err := addToZip(zw, dir, rel); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addToZip(zw *zip.Writer, dir, rel string) error {
	f, err := os.Open(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	hdr.Name, hdr.Method = rel, zip.Deflate
	dst, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, f)
	return err
}

type scanner interface{ Scan(...any) error }

func scan(row scanner) (*Project, error) {
	var p Project
	var created, updated int64
	if err := row.Scan(&p.ID, &p.Name, &p.MainFile, &created, &updated, &p.Owner); err != nil {
		return nil, err
	}
	p.CreatedAt, p.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return &p, nil
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
