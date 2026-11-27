package users

import (
	"cmp"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ponytojas/Refract/internal/auth"
)

var (
	ErrAccountExists = errors.New("an account already has this email address")
	ErrAdminEmail    = errors.New("confirm the account's email address before granting administrator access")
	ErrLastAdmin     = errors.New("the last administrator cannot be deleted")
)

type AdminAccount struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	HasPassword bool   `json:"has_password"`
	Verified    bool   `json:"email_verified"`
	IsAdmin     bool   `json:"is_admin"`
}

func (s *Service) AdminAccounts(ctx context.Context) ([]AdminAccount, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, email, password != '', email_verified_at > 0, is_admin FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AdminAccount{}
	for rows.Next() {
		var a AdminAccount
		if err := rows.Scan(&a.ID, &a.Email, &a.HasPassword, &a.Verified, &a.IsAdmin); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// RemoveKeepingAdmin serializes web removals so each request checks the
// remaining roles before deleting projects. Self-service callers authenticate
// first and pass nil for authorizeAdmin.
func (s *Service) RemoveKeepingAdmin(ctx context.Context, id string, destroy func(context.Context, []string) (int, error), authorizeAdmin func() error) (int, error) {
	s.removalMu.Lock()
	defer s.removalMu.Unlock()
	var admin, otherAdmin bool
	err := s.DB.QueryRowContext(ctx, `SELECT is_admin, EXISTS (SELECT 1 FROM users WHERE is_admin AND id != ?)
		FROM users WHERE id = ?`, id, id).Scan(&admin, &otherAdmin)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoAccount
	}
	if err != nil {
		return 0, err
	}
	if admin {
		if authorizeAdmin != nil {
			if err := authorizeAdmin(); err != nil {
				return 0, err
			}
		}
		if !otherAdmin {
			return 0, ErrLastAdmin
		}
	}
	return s.Remove(ctx, id, destroy)
}

// GrantAdmin is reserved for the operator command.
func (s *Service) GrantAdmin(ctx context.Context, email string) error {
	acct, err := s.Lookup(ctx, email)
	if err != nil {
		return err
	}
	if !acct.Verified {
		return ErrAdminEmail
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET is_admin = 1 WHERE id = ? AND email_verified_at > 0`, acct.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return cmp.Or(err, ErrNoAccount)
	}
	return nil
}

// ProvisionPassword creates a trusted account or resets an existing password.
// With onlyNew, an existing address is refused without changing its password.
func (s *Service) ProvisionPassword(ctx context.Context, email string, onlyNew bool) (auth.User, string, error) {
	email = normEmail(email)
	if err := validEmail(email); err != nil {
		return auth.User{}, "", err
	}
	return s.issuePassword(ctx, email, "", onlyNew)
}

func (s *Service) ResetAccountPassword(ctx context.Context, id string) (string, error) {
	if id == "" {
		return "", ErrNoAccount
	}
	_, password, err := s.issuePassword(ctx, "", id, false)
	return password, err
}

func (s *Service) issuePassword(ctx context.Context, email, id string, onlyNew bool) (auth.User, string, error) {
	password := rand.Text()
	hash, err := hashPassword(password)
	if err != nil {
		return auth.User{}, "", err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return auth.User{}, "", err
	}
	defer tx.Rollback()
	u := auth.User{ID: id, Email: email}
	if id != "" {
		err = tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, id).Scan(&u.Email)
		if errors.Is(err, sql.ErrNoRows) {
			return auth.User{}, "", ErrNoAccount
		}
	} else {
		err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, email).Scan(&u.ID)
		if errors.Is(err, sql.ErrNoRows) {
			u, err = s.createTrusted(ctx, tx, email)
		} else if err == nil && onlyNew {
			return auth.User{}, "", ErrAccountExists
		}
	}
	if err != nil {
		return auth.User{}, "", err
	}
	if err := clearUnverified(ctx, tx, u.ID); err != nil {
		return auth.User{}, "", err
	}
	if err := putPassword(ctx, tx, u.ID, hash); err != nil {
		return auth.User{}, "", err
	}
	if err := s.markVerified(ctx, tx, u.ID); err != nil {
		return auth.User{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return auth.User{}, "", err
	}
	return u, password, nil
}

// RenameAccount changes the address of a particular account and revokes its
// sessions and links issued for the prior address in the same transaction.
func (s *Service) RenameAccount(ctx context.Context, id, to string) error {
	to = normEmail(to)
	if err := validEmail(to); err != nil {
		return err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, id).Scan(&current); errors.Is(err, sql.ErrNoRows) {
		return ErrNoAccount
	} else if err != nil {
		return err
	}
	if current == to {
		return nil
	}
	var one int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM users WHERE email = ?`, to).Scan(&one)
	if err == nil {
		return ErrAccountExists
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE users SET email = ? WHERE id = ?`, to, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return cmp.Or(err, ErrNoAccount)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) SignOutAccount(ctx context.Context, id string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Rename keeps the same account and data when changing its sign-in address.
func (s *Service) Rename(ctx context.Context, from, to string) error {
	acct, err := s.Lookup(ctx, from)
	if errors.Is(err, ErrNoAccount) {
		return fmt.Errorf("there is no account %q", from)
	}
	if err != nil {
		return err
	}
	return s.RenameAccount(ctx, acct.ID, to)
}
