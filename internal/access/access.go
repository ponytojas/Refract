// Package access decides what the request's user may do with a project. It
// is the one check every project, chat and turn route goes through (see the
// route table in cmd/refract), and it fails closed: no user, an unknown
// project or anything unexpected means no access. Projects in the trash are
// not found, even by their owner; only the two trash routes see them.
package access

import (
	"context"
	"database/sql"
	"errors"

	"github.com/ponytojas/Refract/internal/auth"
)

// Role is what someone may do with a project. Higher roles include lower ones.
type Role int

const (
	None   Role = iota // the project doesn't exist for them
	Viewer             // read only (shared projects, T6a)
	Editor             // shared editing, not used yet
	Owner
)

var (
	// ErrNotFound answers anyone without a role, so a project's existence
	// isn't revealed to strangers.
	ErrNotFound = errors.New("not found")
	// ErrForbidden answers someone who can see the project but asks for more.
	ErrForbidden = errors.New("forbidden")
	ErrNoUser    = errors.New("no signed-in user")
)

// UserFrom is the request's user. An empty id counts as no user: projects
// created before the first account have owner "", and must not match it.
func UserFrom(ctx context.Context) (auth.User, error) {
	u, ok := auth.UserFrom(ctx)
	if !ok || u.ID == "" {
		return auth.User{}, ErrNoUser
	}
	return u, nil
}

type Checker struct{ DB *sql.DB }

// ProjectRole is the user's role on the project; None when there is no user,
// no such project, or the project is in the trash.
func (c *Checker) ProjectRole(ctx context.Context, projectID string) (Role, error) {
	return c.role(ctx, projectID, "deleted_at = 0")
}

// TrashedRole is the user's role on a project in the trash: Owner for its
// owner; None for anyone else, and for projects that are not in the trash.
func (c *Checker) TrashedRole(ctx context.Context, projectID string) (Role, error) {
	return c.role(ctx, projectID, "deleted_at > 0")
}

// role finds the project under cond, which is always one of the constants
// above, never user input.
func (c *Checker) role(ctx context.Context, projectID, cond string) (Role, error) {
	u, err := UserFrom(ctx)
	if err != nil {
		return None, nil
	}
	var owner string
	switch err := c.DB.QueryRowContext(ctx, `SELECT owner FROM projects WHERE id = ? AND `+cond, projectID).Scan(&owner); {
	case errors.Is(err, sql.ErrNoRows):
		return None, nil
	case err != nil:
		return None, err
	}
	if owner == u.ID {
		return Owner, nil
	}
	return None, nil
}

// Require returns nil when the user has at least role min on the project.
func (c *Checker) Require(ctx context.Context, projectID string, min Role) error {
	role, err := c.ProjectRole(ctx, projectID)
	if err != nil {
		return err
	}
	return allow(role, min)
}

// RequireTrashed is Require for a project in the trash.
func (c *Checker) RequireTrashed(ctx context.Context, projectID string, min Role) error {
	role, err := c.TrashedRole(ctx, projectID)
	if err != nil {
		return err
	}
	return allow(role, min)
}

func allow(role, min Role) error {
	switch {
	case role == None:
		return ErrNotFound
	case role < min:
		return ErrForbidden
	}
	return nil
}

// ChatProject is the project a chat belongs to.
func (c *Checker) ChatProject(ctx context.Context, chatID string) (string, error) {
	return c.lookup(ctx, `SELECT project_id FROM chats WHERE id = ?`, chatID)
}

// TurnProject is the project a turn's chat belongs to.
func (c *Checker) TurnProject(ctx context.Context, turnID string) (string, error) {
	return c.lookup(ctx, `SELECT c.project_id FROM turns t JOIN chats c ON c.id = t.chat_id WHERE t.id = ?`, turnID)
}

func (c *Checker) lookup(ctx context.Context, query, id string) (string, error) {
	var project string
	err := c.DB.QueryRowContext(ctx, query, id).Scan(&project)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return project, err
}
