package siwc

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/store"
)

// ErrNotSignedIn means there are no usable credentials: run `refract login` and import them.
var ErrNotSignedIn = errors.New("not signed in to ChatGPT")

const (
	credKey    = "siwc.credentials"
	hostKey    = "siwc.host_id"
	welcomeKey = "siwc.welcome_seen"

	refreshMargin = 5 * time.Minute
)

// Session is one user's ChatGPT registration: encrypted storage, serialized
// rotating refresh and sign-out. The host id is the server's, shared by all.
type Session struct {
	DB             *sql.DB
	Key            []byte // AES-256 key for credentials at rest
	OAuth          *OAuth
	AllowedSubject string // REFRACT_ALLOWED_SUBJECT: if set, the only ChatGPT account accepted
	Owner          string // user id; its settings are scoped to it

	// mu serializes every read-modify-write of the credentials, so concurrent
	// requests never race a rotating refresh token: one refreshes, the rest wait.
	mu sync.Mutex
}

// HostID returns this server's ext_agent_host_id, creating it on first use.
// Imported credentials never carry over the laptop's host id.
func (s *Session) HostID(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := store.GetSetting(ctx, s.DB, hostKey)
	if err != nil || id != "" {
		return id, err
	}
	id = NewHostID()
	return id, store.SetSetting(ctx, s.DB, hostKey, id)
}

// key scopes a setting to the session's user.
func (s *Session) key(k string) string {
	if s.Owner == "" {
		return k
	}
	return store.UserKey(s.Owner, k)
}

// Import validates a credential file written by `refract login` and stores it
// for this user. The ID token signature proves which account it belongs to;
// with REFRACT_ALLOWED_SUBJECT set, only that account is accepted.
func (s *Session) Import(ctx context.Context, raw []byte) (*Credentials, error) {
	var c Credentials
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("not a credential file: %w", err)
	}
	if c.ClientID == "" || c.RefreshToken == "" || c.IDToken == "" {
		return nil, errors.New("credential file is missing client_id, refresh_token or id_token")
	}
	idt, err := s.OAuth.VerifyIDToken(ctx, c.ClientID, c.IDToken, true)
	if err != nil {
		return nil, fmt.Errorf("id token: %w", err)
	}
	if s.AllowedSubject != "" && idt.Subject != s.AllowedSubject {
		return nil, errors.New("this ChatGPT account is not allowed on this server (REFRACT_ALLOWED_SUBJECT)")
	}
	c.Subject = idt.Subject // trust the signed token, not the file's field
	var claims struct {
		Email string `json:"email"`
	}
	idt.Claims(&claims)
	c.Email = claims.Email

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.save(ctx, &c); err != nil {
		return nil, err
	}
	return &c, nil
}

// Credentials returns the stored record (nil if none) for status display. Never send tokens to the browser.
func (s *Session) Credentials(ctx context.Context) (*Credentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load(ctx)
}

// AccessToken returns a valid access token, refreshing first if it is near
// expiry or force is set (e.g. after a 401).
func (s *Session) AccessToken(ctx context.Context, force bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(ctx)
	if err != nil {
		return "", err
	}
	if c == nil || c.RefreshToken == "" {
		return "", ErrNotSignedIn
	}
	now := time.Now()
	stillValid := now.Before(c.ExpiresAt.Add(-refreshMargin))
	tooEarly := now.Before(c.EarliestRefreshAt) && now.Before(c.ExpiresAt)
	if !force && (stillValid || tooEarly) {
		return c.AccessToken, nil
	}

	// Not cancellable by the caller: once the server rotates the token, losing
	// the response (or failing to save it) burns the session. OAuth.HTTP's
	// timeout still bounds it.
	ctx = context.WithoutCancel(ctx)
	next, err := s.OAuth.Refresh(ctx, c)
	if err != nil {
		var te *TokenError
		if errors.As(err, &te) && te.Terminal() {
			// Keep the registration (client id, identity); drop the dead tokens.
			c.AccessToken, c.RefreshToken = "", ""
			if serr := s.save(ctx, c); serr != nil {
				return "", serr
			}
			return "", fmt.Errorf("%w: %s", ErrNotSignedIn, te.Code)
		}
		return "", fmt.Errorf("refresh: %w", err) // transient: keep credentials, caller may retry
	}
	// The old refresh token is spent: persist the new set before using it.
	if err := s.save(ctx, next); err != nil {
		slog.Error("siwc: rotated tokens could not be saved; a new login will be needed", "err", err)
		return "", err
	}
	return next.AccessToken, nil
}

// ExpireNow marks the access token expired so the next call refreshes. For tests.
func (s *Session) ExpireNow(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(ctx)
	if err != nil {
		return err
	}
	if c == nil || c.RefreshToken == "" {
		return ErrNotSignedIn
	}
	c.ExpiresAt, c.EarliestRefreshAt = time.Now().Add(-time.Minute), time.Time{}
	return s.save(ctx, c)
}

// SignOut revokes the refresh token and clears tokens locally even if revocation
// fails; the returned error then tells the user to disconnect in ChatGPT settings.
func (s *Session) SignOut(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, err := s.load(ctx)
	if err != nil || c == nil || c.RefreshToken == "" {
		return err
	}
	revokeErr := s.OAuth.Revoke(ctx, c)
	c.AccessToken, c.RefreshToken = "", ""
	if err := s.save(ctx, c); err != nil {
		return err
	}
	if revokeErr != nil {
		return fmt.Errorf("signed out locally, but revocation was not confirmed (%v); disconnect the app in ChatGPT settings to be sure", revokeErr)
	}
	return nil
}

// WelcomeSeen reports whether the first-sign-in notice was dismissed; MarkWelcomeSeen records it.
func (s *Session) WelcomeSeen(ctx context.Context) (bool, error) {
	v, err := store.GetSetting(ctx, s.DB, s.key(welcomeKey))
	return v != "", err
}

func (s *Session) MarkWelcomeSeen(ctx context.Context) error {
	return store.SetSetting(ctx, s.DB, s.key(welcomeKey), "1")
}

func (s *Session) load(ctx context.Context) (*Credentials, error) {
	sealed, err := store.GetSetting(ctx, s.DB, s.key(credKey))
	if err != nil || sealed == "" {
		return nil, err
	}
	pt, err := store.Unseal(s.Key, sealed, s.key(credKey))
	if err != nil {
		return nil, err
	}
	var c Credentials
	return &c, json.Unmarshal(pt, &c)
}

func (s *Session) save(ctx context.Context, c *Credentials) error {
	pt, err := json.Marshal(c)
	if err != nil {
		return err
	}
	sealed, err := store.Seal(s.Key, pt, s.key(credKey))
	if err != nil {
		return err
	}
	return store.SetSetting(ctx, s.DB, s.key(credKey), sealed)
}
