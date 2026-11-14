package projects

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponytojas/Refract/internal/access"
)

// TrashDays is how long a deleted project stays in the trash.
const TrashDays = 30

// ErrTurnRunning is returned when a project can't leave its user because the
// assistant is working on it.
var ErrTurnRunning = errors.New("the assistant is working on this project: stop it or wait until it finishes, then try again")

// purgePrefix marks a project directory that is being deleted for good.
const purgePrefix = ".purge-"

// TrashedProject is a project in the trash.
type TrashedProject struct {
	Project
	DeletedAt time.Time `json:"deleted_at"`
	PurgeAt   time.Time `json:"purge_at"` // DeletedAt + TrashDays; the daily purge removes it within a day after
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// holdTurns keeps new assistant turns off the project; ok is false while one runs.
func (s *Service) holdTurns(id string) (release func(), ok bool) {
	if s.HoldTurns == nil {
		return func() {}, true
	}
	return s.HoldTurns(id)
}

// Trash moves a project to the trash. Files, chats and the remote setting stay,
// so a restore brings everything back. It fails with ErrTurnRunning while the
// assistant works on the project.
//
// Everything that takes a project away from its user takes lockRemote first
// (so a push or pull in progress finishes, and a waiting one finds nothing),
// then holds turns, then lockRepo for the change itself, never the other way.
func (s *Service) Trash(ctx context.Context, id string) error {
	defer lockRemote(id)()
	release, ok := s.holdTurns(id)
	if !ok {
		return ErrTurnRunning
	}
	defer release()
	defer lockRepo(id)()
	res, err := s.DB.ExecContext(ctx, `UPDATE projects SET deleted_at = ? WHERE id = ? AND deleted_at = 0`, s.now().Unix(), id)
	return foundOne(res, err)
}

// Restore takes a project out of the trash. updated_at is unchanged, so it
// returns to its old place in the list. A trashed project can't have a turn,
// so there is nothing to hold.
func (s *Service) Restore(ctx context.Context, id string) error {
	defer lockRemote(id)()
	defer lockRepo(id)()
	res, err := s.DB.ExecContext(ctx, `UPDATE projects SET deleted_at = 0 WHERE id = ? AND deleted_at > 0`, id)
	return foundOne(res, err)
}

func foundOne(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Purge deletes a project in the trash for good. A live project is never
// deleted by it: ErrNotFound.
func (s *Service) Purge(ctx context.Context, id string) error {
	return s.purge(ctx, id, "deleted_at > 0")
}

// Destroy deletes a project for good, live or in the trash (account removal).
func (s *Service) Destroy(ctx context.Context, id string) error {
	return s.purge(ctx, id, "1 = 1")
}

// PurgeExpired deletes the projects that have been in the trash for more than
// TrashDays, and returns how many. One failure doesn't stop the rest.
func (s *Service) PurgeExpired(ctx context.Context) (int, error) {
	var errs []error
	if err := s.sweepPurges(ctx); err != nil {
		errs = append(errs, err)
	}
	cutoff := s.now().Add(-TrashDays * 24 * time.Hour).Unix()
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM projects WHERE deleted_at > 0 AND deleted_at <= ?`, cutoff)
	if err != nil {
		return 0, errors.Join(append(errs, err)...)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, errors.Join(append(errs, err)...)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, errors.Join(append(errs, err)...)
	}
	n := 0
	for _, id := range ids {
		// The condition is checked again under the lock: a restore in between wins.
		switch err := s.purge(ctx, id, "deleted_at > 0 AND deleted_at <= ?", cutoff); {
		case err == nil:
			n++
		case errors.Is(err, ErrNotFound):
		default:
			errs = append(errs, err)
		}
	}
	return n, errors.Join(errs...)
}

// PurgeTrashEvery purges expired trash now and then every interval until ctx
// ends. It runs without a user, like assistant.Recover: system work.
func (s *Service) PurgeTrashEvery(ctx context.Context, every time.Duration) {
	run := func() {
		n, err := s.PurgeExpired(ctx)
		if n > 0 {
			slog.Info("purged expired projects from the trash", "count", n)
		}
		if err != nil && ctx.Err() == nil {
			slog.Error("purging the trash", "err", err)
		}
	}
	run()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// purge deletes a project whose row matches cond (a constant, never user
// input) with its files, chats, turns and remote setting. The directory is
// renamed out of the way first, so every failure point either heals itself
// (sweepPurges) or leaves a clearly marked directory.
func (s *Service) purge(ctx context.Context, id, cond string, args ...any) error {
	defer lockRemote(id)()
	release, ok := s.holdTurns(id)
	if !ok {
		return ErrTurnRunning
	}
	defer release()

	gone := filepath.Join(s.Dir, purgePrefix+id)
	err := func() error {
		defer lockRepo(id)()
		var one int
		switch err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ? AND `+cond, append([]any{id}, args...)...).Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			return ErrNotFound
		case err != nil:
			return err
		}
		if err := os.Rename(s.Path(id), gone); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err // an earlier purge may have moved it already
		}
		back := func() {
			if err := os.Rename(gone, s.Path(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
				slog.Error("restoring a project directory after a failed purge", "project", id, "err", err)
			}
		}
		tx, err := s.DB.BeginTx(ctx, nil)
		if err != nil {
			back()
			return err
		}
		defer tx.Rollback()
		res, err := tx.ExecContext(ctx, `DELETE FROM projects WHERE id = ? AND `+cond, append([]any{id}, args...)...)
		if err == nil {
			if n, _ := res.RowsAffected(); n == 0 {
				err = ErrNotFound
			}
		}
		if err == nil {
			// The sealed remote and its token go with the project, not with the trash.
			_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, remoteKey(id))
		}
		if err == nil {
			err = tx.Commit()
		}
		if err != nil {
			back()
			return err
		}
		return nil
	}()
	if err != nil {
		return err
	}
	if err := os.RemoveAll(gone); err != nil {
		// The project is gone for its user; the next sweep finishes the job.
		slog.Warn("removing a purged project's files", "project", id, "err", err)
	}
	return nil
}

// sweepPurges finishes interrupted purges. A .purge-<id> directory with no row
// is what a purge left behind; with a row, the server stopped between the
// rename and the delete, so the directory goes back. It never deletes a
// directory just because it has no row: that would wipe every project after
// an older database is restored, and race create, which renames before it
// inserts.
func (s *Service) sweepPurges(ctx context.Context) error {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		id, ok := strings.CutPrefix(e.Name(), purgePrefix)
		if !ok || !e.IsDir() || id == "" {
			continue
		}
		if err := s.sweepOne(ctx, id); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Service) sweepOne(ctx context.Context, id string) error {
	defer lockRemote(id)()
	defer lockRepo(id)()
	gone := filepath.Join(s.Dir, purgePrefix+id)
	var one int
	switch err := s.DB.QueryRowContext(ctx, `SELECT 1 FROM projects WHERE id = ?`, id).Scan(&one); {
	case errors.Is(err, sql.ErrNoRows):
		return os.RemoveAll(gone)
	case err != nil:
		return err
	}
	if _, err := os.Stat(s.Path(id)); err == nil {
		return nil // both exist: leave them for a person to look at
	}
	return os.Rename(gone, s.Path(id))
}

// ListTrash returns the request user's projects in the trash, newest first;
// none without a user.
func (s *Service) ListTrash(ctx context.Context) ([]TrashedProject, error) {
	u, err := access.UserFrom(ctx)
	if err != nil {
		return []TrashedProject{}, nil
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+projectCols+`, deleted_at FROM projects WHERE owner = ? AND deleted_at > 0 ORDER BY deleted_at DESC`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TrashedProject{}
	for rows.Next() {
		var deleted int64
		p, err := scan(rows, &deleted)
		if err != nil {
			return nil, err
		}
		at := time.Unix(deleted, 0)
		out = append(out, TrashedProject{Project: *p, DeletedAt: at, PurgeAt: at.Add(TrashDays * 24 * time.Hour)})
	}
	return out, rows.Err()
}
