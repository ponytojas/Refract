package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
)

// Single-use links sent by email: confirming an address and choosing a new
// password. The database keeps only a digest of each token.

type Purpose string

const (
	Verify Purpose = "verify" // confirms the email address
	Reset  Purpose = "reset"  // chooses a new password

	VerifyTTL = 24 * time.Hour
	ResetTTL  = time.Hour
)

var (
	ErrBadToken         = errors.New("this link has expired or was already used")
	ErrPasswordRequired = errors.New("enter your password to confirm your email")
)

func (p Purpose) ttl() time.Duration {
	if p == Reset {
		return ResetTTL
	}
	return VerifyTTL
}

// issue makes a token for a link: 32 random bytes, base64url. Only its digest is
// stored. The user's earlier tokens of the same purpose, and every expired token,
// are deleted, so only the newest link works.
func (s *Service) issue(ctx context.Context, tx *sql.Tx, userID string, p Purpose) (string, error) {
	now := s.now()
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE expires_at <= ? OR (user_id = ? AND purpose = ?)`, now.Unix(), userID, p); err != nil {
		return "", err
	}
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	_, err := tx.ExecContext(ctx, `INSERT INTO account_tokens (hash, user_id, purpose, expires_at) VALUES (?, ?, ?, ?)`,
		digest(token), userID, p, now.Add(p.ttl()).Unix())
	return token, err
}

// redeem marks a token used, once, and returns its account.
func (s *Service) redeem(ctx context.Context, tx *sql.Tx, token string, p Purpose) (string, error) {
	var id string
	now := s.now().Unix()
	err := tx.QueryRowContext(ctx, `UPDATE account_tokens SET used_at = ? WHERE hash = ? AND purpose = ? AND used_at = 0 AND expires_at > ? RETURNING user_id`,
		now, digest(token), p, now).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrBadToken
	}
	return id, err
}

// tokenAccount reads a valid token's account without using it up.
func (s *Service) tokenAccount(ctx context.Context, token string, p Purpose) (Account, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `SELECT user_id FROM account_tokens WHERE hash = ? AND purpose = ? AND used_at = 0 AND expires_at > ?`,
		digest(token), p, s.now().Unix()).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrBadToken
	}
	if err != nil {
		return Account{}, err
	}
	return s.AccountByID(ctx, id)
}

// Link is an email a flow asks the caller to send.
type Link struct {
	To      Account
	Purpose Purpose
	Token   string
	SignUp  bool // a reset sent because someone signed up with an address that has an account
}

// ValidateSignUp checks what SignUp checks of its input, which depends on
// nothing but the input: callers use it to answer 400 before anything else.
func ValidateSignUp(email, pw, lang string) error {
	if err := validEmail(normEmail(email)); err != nil {
		return err
	}
	if err := validPassword(pw); err != nil {
		return err
	}
	if !validLang(lang) {
		return errors.New("choose a language for emails")
	}
	return nil
}

// ValidateEmail checks that email looks like an address.
func ValidateEmail(email string) error { return validEmail(normEmail(email)) }

// SignUp validates the input, then always hashes pw. For a new address it creates
// an unverified account (password, lang; no adoption of earlier data) and returns
// a Verify link. For an address with an account it changes nothing and returns a
// Reset link with SignUp set. Callers must answer both cases identically.
func (s *Service) SignUp(ctx context.Context, email, pw, lang string) (Link, error) {
	email = normEmail(email)
	if err := ValidateSignUp(email, pw, lang); err != nil {
		return Link{}, err
	}
	h, err := hashPassword(pw)
	if err != nil {
		return Link{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Link{}, err
	}
	defer tx.Rollback()
	var l Link
	err = tx.QueryRowContext(ctx, `SELECT id, email, lang, password != '', email_verified_at > 0 FROM users WHERE email = ?`, email).
		Scan(&l.To.ID, &l.To.Email, &l.To.Lang, &l.To.HasPassword, &l.To.Verified)
	switch {
	case err == nil: // exists: change nothing, offer the way back in
		l.Purpose, l.SignUp = Reset, true
	case errors.Is(err, sql.ErrNoRows):
		l.To = Account{ID: newID(), Email: email, Lang: lang, HasPassword: true}
		l.Purpose = Verify
		if _, err := tx.ExecContext(ctx, `INSERT INTO users (id, email, created_at, password, lang) VALUES (?, ?, ?, ?, ?)`,
			l.To.ID, email, s.now().Unix(), h, lang); err != nil {
			return Link{}, err
		}
	default:
		return Link{}, err
	}
	if l.Token, err = s.issue(ctx, tx, l.To.ID, l.Purpose); err != nil {
		return Link{}, err
	}
	return l, tx.Commit()
}

// RequestReset returns a Reset link for the address, or ok=false when it has no account.
func (s *Service) RequestReset(ctx context.Context, email string) (l Link, ok bool, err error) {
	a, err := s.Lookup(ctx, email)
	if errors.Is(err, ErrNoAccount) {
		return Link{}, false, nil
	}
	if err != nil {
		return Link{}, false, err
	}
	return s.link(ctx, a, Reset)
}

// ResendVerification returns a new Verify link, or ok=false when the account is already verified.
func (s *Service) ResendVerification(ctx context.Context, userID string) (l Link, ok bool, err error) {
	a, err := s.AccountByID(ctx, userID)
	if err != nil || a.Verified {
		return Link{}, false, err
	}
	return s.link(ctx, a, Verify)
}

func (s *Service) link(ctx context.Context, a Account, p Purpose) (Link, bool, error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return Link{}, false, err
	}
	defer tx.Rollback()
	tok, err := s.issue(ctx, tx, a.ID, p)
	if err != nil {
		return Link{}, false, err
	}
	return Link{To: a, Purpose: p, Token: tok}, true, tx.Commit()
}

// ResetPassword validates pw, hashes it, then in one transaction: redeems the
// token, sets the password, marks the account verified, ends every session and
// deletes the account's remaining links.
func (s *Service) ResetPassword(ctx context.Context, token, pw string) (auth.User, error) {
	if err := validPassword(pw); err != nil {
		return auth.User{}, err
	}
	h, err := hashPassword(pw)
	if err != nil {
		return auth.User{}, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return auth.User{}, err
	}
	defer tx.Rollback()
	var u auth.User
	if u.ID, err = s.redeem(ctx, tx, token, Reset); err != nil {
		return auth.User{}, err
	}
	if err := tx.QueryRowContext(ctx, `SELECT email FROM users WHERE id = ?`, u.ID).Scan(&u.Email); err != nil {
		return auth.User{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE users SET password = ? WHERE id = ?`, h, u.ID); err != nil {
		return auth.User{}, err
	}
	if err := s.markVerified(ctx, tx, u.ID); err != nil {
		return auth.User{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, u.ID); err != nil {
		return auth.User{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ?`, u.ID); err != nil {
		return auth.User{}, err
	}
	return u, tx.Commit()
}

// VerifyEmail confirms the token's account. Unless signedInAs is that account
// (or it has no password), it first checks pw through Login (so the lockout
// applies); an empty pw is ErrPasswordRequired and leaves the token usable.
// Then, in one transaction: redeem, mark verified, delete the other verify links.
func (s *Service) VerifyEmail(ctx context.Context, token, signedInAs, pw, ip string) (auth.User, error) {
	a, err := s.tokenAccount(ctx, token, Verify)
	if err != nil {
		return auth.User{}, err
	}
	if a.HasPassword && signedInAs != a.ID {
		if pw == "" {
			return auth.User{}, ErrPasswordRequired
		}
		if _, err := s.Login(ctx, a.Email, pw, ip); err != nil {
			return auth.User{}, err
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return auth.User{}, err
	}
	defer tx.Rollback()
	id, err := s.redeem(ctx, tx, token, Verify)
	if err != nil {
		return auth.User{}, err
	}
	if err := s.markVerified(ctx, tx, id); err != nil {
		return auth.User{}, err
	}
	return auth.User{ID: id, Email: a.Email}, tx.Commit()
}

// ConfirmByProvider is called when an identity provider vouches for the email of
// an account that was never verified: someone may have signed up with a password
// for an address that isn't theirs, so its password, sessions and links go, and
// it becomes verified. No-op for a verified account.
func (s *Service) ConfirmByProvider(ctx context.Context, userID string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var verified bool
	if err := tx.QueryRowContext(ctx, `SELECT email_verified_at > 0 FROM users WHERE id = ?`, userID).Scan(&verified); err != nil {
		return err
	}
	if verified {
		return nil
	}
	for _, q := range []string{
		`UPDATE users SET password = '' WHERE id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM account_tokens WHERE user_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, userID); err != nil {
			return err
		}
	}
	if err := s.markVerified(ctx, tx, userID); err != nil {
		return err
	}
	return tx.Commit()
}
