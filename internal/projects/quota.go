package projects

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
)

// Quota caps what one account may hold. A zero field means no limit.
type Quota struct {
	MaxProjects int
	MaxBytes    int64 // all of the account's projects, with history and trash
}

// ErrOverLimit refuses an operation that would add to an account at its limit.
var ErrOverLimit = errors.New("limit reached")

// checkQuota refuses when the account is at or over a limit. Trashed projects
// count: they are still on disk. It is never called on reads or deletes.
func (s *Service) checkQuota(ctx context.Context, owner string, adding bool) error {
	if s.QuotaFor == nil {
		return nil
	}
	q, err := s.QuotaFor(ctx, owner)
	if err != nil {
		return err
	}
	if q.MaxProjects == 0 && q.MaxBytes == 0 {
		return nil
	}
	var count int
	var sum int64
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(size_bytes), 0) FROM projects WHERE owner = ?`, owner).Scan(&count, &sum); err != nil {
		return err
	}
	if adding && q.MaxProjects > 0 && count >= q.MaxProjects {
		return fmt.Errorf("%w: this account has its %d projects; delete one to add another", ErrOverLimit, q.MaxProjects)
	}
	if q.MaxBytes > 0 && sum >= q.MaxBytes {
		return fmt.Errorf("%w: this account's storage is full (%d MB); delete files or projects to continue", ErrOverLimit, q.MaxBytes>>20)
	}
	return nil
}

// checkQuotaFor is checkQuota for the owner of project id.
func (s *Service) checkQuotaFor(ctx context.Context, id string) error {
	if s.QuotaFor == nil {
		return nil
	}
	p, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	return s.checkQuota(ctx, p.Owner, false)
}

// measure stores what the project takes on disk, history and turn worktrees
// included. Failures are logged: a failed measurement must not fail a save.
func (s *Service) measure(ctx context.Context, id string) {
	if s.QuotaFor == nil {
		return
	}
	var total int64
	err := filepath.WalkDir(s.Path(id), func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	if err == nil {
		_, err = s.DB.ExecContext(ctx, `UPDATE projects SET size_bytes = ? WHERE id = ?`, total, id)
	}
	if err != nil {
		slog.Warn("measuring project", "project", id, "err", err)
	}
}

// grew adds n bytes to the stored size: an upper bound until the next measure.
func (s *Service) grew(ctx context.Context, id string, n int64) {
	if s.QuotaFor == nil {
		return
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE projects SET size_bytes = size_bytes + ? WHERE id = ?`, n, id); err != nil {
		slog.Warn("sizing project", "project", id, "err", err)
	}
}

// MeasureAll measures every project (live and trashed), for those that
// existed before sizes were tracked.
func (s *Service) MeasureAll(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM projects`)
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	for _, id := range ids {
		s.measure(ctx, id)
	}
	return nil
}
