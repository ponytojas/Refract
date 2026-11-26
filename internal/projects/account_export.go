package projects

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ExportProject describes one project in an account archive, including its
// deletion time if it is in the trash.
type ExportProject struct {
	Project
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

func (s *Service) AccountProjects(ctx context.Context, owner string) ([]ExportProject, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+projectCols+`, deleted_at FROM projects WHERE owner = ? ORDER BY id`, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportProject{}
	for rows.Next() {
		var deleted int64
		p, err := scan(rows, &deleted)
		if err != nil {
			return nil, err
		}
		item := ExportProject{Project: *p}
		if deleted > 0 {
			at := time.Unix(deleted, 0).UTC()
			item.DeletedAt = &at
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// WriteAccountProject writes the complete reachable git history and a copy
// of the current files, including edits made since the last checkpoint.
func (s *Service) WriteAccountProject(ctx context.Context, zw *zip.Writer, owner, id string) error {
	if !filepath.IsLocal(id) || filepath.Base(id) != id || id == "." || strings.Contains(id, "\\") {
		return fmt.Errorf("invalid project ID %q", id)
	}
	defer lockRepo(id)()
	var exists int
	err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ? AND owner = ?`, id, owner).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "refract-git-bundle-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	bundle := filepath.Join(dir, "history.bundle")
	repo := s.Path(id)
	if _, err := git(ctx, repo, "bundle", "create", bundle, "--all", "HEAD"); err != nil {
		return err
	}
	f, err := os.Open(bundle)
	if err != nil {
		return err
	}
	defer f.Close()
	prefix := "projects/" + id + "/"
	// A git bundle is already compressed.
	dst, err := zw.CreateHeader(&zip.FileHeader{Name: prefix + "history.bundle", Method: zip.Store})
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, f); err != nil {
		return err
	}
	return writeFilesToZip(ctx, zw, repo, prefix+"files/")
}
