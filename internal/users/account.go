package users

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Account is what the account screens and emails need to know about a user.
type Account struct {
	ID, Email   string
	Lang        string // "en", "es", or "" while unknown (emails then go in English)
	HasPassword bool
	Verified    bool
}

var ErrNoAccount = errors.New("there is no such account")

// RecentAuth is how recent a sign-in must be for deleting an account that has
// no password to ask for.
const RecentAuth = 10 * time.Minute

const accountColumns = `SELECT id, email, lang, password != '', email_verified_at > 0 FROM users`

func scanAccount(row *sql.Row) (Account, error) {
	var a Account
	err := row.Scan(&a.ID, &a.Email, &a.Lang, &a.HasPassword, &a.Verified)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoAccount
	}
	return a, err
}

// Lookup returns the account with this email (any case); ErrNoAccount if none.
func (s *Service) Lookup(ctx context.Context, email string) (Account, error) {
	return scanAccount(s.DB.QueryRowContext(ctx, accountColumns+` WHERE email = ?`, normEmail(email)))
}

func (s *Service) AccountByID(ctx context.Context, id string) (Account, error) {
	return scanAccount(s.DB.QueryRowContext(ctx, accountColumns+` WHERE id = ?`, id))
}

func validLang(lang string) bool { return lang == "en" || lang == "es" }

// SetLang sets the language of an account's emails: "en" or "es".
func (s *Service) SetLang(ctx context.Context, userID, lang string) error {
	if !validLang(lang) {
		return fmt.Errorf("unknown language %q", lang)
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE users SET lang = ? WHERE id = ?`, lang, userID)
	return err
}

// MarkVerified verifies an account (no-op when it already is) and drops its verify links.
func (s *Service) MarkVerified(ctx context.Context, userID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.markVerified(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Service) markVerified(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `UPDATE users SET email_verified_at = ? WHERE id = ? AND email_verified_at = 0`, max(s.now().Unix(), 1), userID); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ? AND purpose = ?`, userID, Verify)
	return err
}

// AuthenticatedAt is when the session's sign-in happened (zero for sessions from before A3).
func (s *Service) AuthenticatedAt(ctx context.Context, token string) (time.Time, error) {
	var at int64
	err := s.DB.QueryRowContext(ctx, `SELECT authenticated_at FROM sessions WHERE id = ? AND expires_at > ?`, sessionID(token), s.now().Unix()).Scan(&at)
	if err != nil || at == 0 {
		return time.Time{}, err
	}
	return time.Unix(at, 0), nil
}
