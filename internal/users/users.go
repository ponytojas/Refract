// Package users maps the identity the auth layer established (an email) to a
// Refract user, creating it on first sight: who may sign in at all is decided
// by the auth layer (Cloudflare Access policy, a single local user, or the
// accounts and sessions kept here for REFRACT_AUTH=password).
package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

type Service struct {
	DB  *sql.DB
	Key []byte // to re-seal adopted credentials
}

// legacy settings of the single-user version, now per user: key → sealed?
var legacy = map[string]bool{"siwc.credentials": true, "siwc.welcome_seen": false, "github": true}

// Resolve returns the user for email, creating it if needed. The first user
// adopts everything that predates users: projects and per-user settings.
func (s *Service) Resolve(ctx context.Context, email string) (auth.User, error) {
	if email == "" {
		return auth.User{}, errors.New("no identity")
	}
	// Looked up on every request (no cache), so removing or renaming an
	// account takes effect at once, also in the running server.
	u := auth.User{Email: email}
	err := s.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&u.ID)
	if err == nil {
		return u, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil) // immediate: two first requests can't both create
	if err != nil {
		return auth.User{}, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&u.ID)
	if errors.Is(err, sql.ErrNoRows) {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			return auth.User{}, err
		}
		u.ID = newID()
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, email, created_at) VALUES (?, ?, ?)`, u.ID, email, time.Now().Unix()); err != nil {
			return auth.User{}, err
		}
		if n == 0 {
			if err := s.adopt(ctx, tx, u.ID); err != nil {
				return auth.User{}, err
			}
		}
	} else if err != nil {
		return auth.User{}, err
	}
	if err := tx.Commit(); err != nil {
		return auth.User{}, err
	}
	return u, nil
}

func (s *Service) adopt(ctx context.Context, tx *sql.Tx, id string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE projects SET owner = ? WHERE owner = ''`, id); err != nil {
		return err
	}
	for key, sealed := range legacy {
		var v string
		err := tx.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			return err
		}
		if sealed { // the old ciphertext is bound to the old key name
			plain, err := store.Unseal(s.Key, v, key)
			if err != nil {
				return err
			}
			if v, err = store.Seal(s.Key, plain, store.UserKey(id, key)); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, ?)`, store.UserKey(id, key), v); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return err
		}
	}
	return nil
}

// Middleware turns the auth layer's identity into the request's user.
func (s *Service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.Resolve(r.Context(), auth.Identity(r.Context()))
		if err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
