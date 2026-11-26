package users

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
)

// Sessions, for REFRACT_AUTH=builtin. A session ends 30 days after it began,
// or after 7 days without use, whichever comes first.

const (
	// SessionCookie is the cookie's name over http (local use). Over https it
	// is SecureSessionCookie: the __Host- prefix makes browsers refuse it
	// unless it is Secure, for the whole site and set by this host only.
	SessionCookie       = "refract_session"
	SecureSessionCookie = "__Host-refract_session"

	SessionTTL  = 30 * 24 * time.Hour
	SessionIdle = 7 * 24 * time.Hour
	// seenEvery is how stale last_seen_at may get before a request updates it,
	// so ordinary requests don't take the database's write lock.
	seenEvery = time.Hour

	maxUserAgent = 256
)

var ErrNoSession = errors.New("no such session")

// Client is where a request came from, as a session remembers it.
type Client struct {
	IP        string
	UserAgent string
}

// ClientOf describes the request's client.
func ClientOf(r *http.Request) Client {
	ua := r.UserAgent()
	if len(ua) > maxUserAgent {
		ua = ua[:maxUserAgent]
	}
	ua = strings.ToValidUTF8(ua, "") // also drops a character the cut split
	return Client{IP: ClientIP(r), UserAgent: ua}
}

// SessionInfo is one session as its owner sees it. ID is the database's id
// (a digest of the token), which can't be used as a cookie.
type SessionInfo struct {
	ID         string    `json:"id"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	UserAgent  string    `json:"user_agent"`
	IP         string    `json:"ip"`
	Current    bool      `json:"current"`
}

// NewSession signs a user in and returns the cookie's token, with no client
// details (the CLI and tests).
func (s *Service) NewSession(ctx context.Context, userID string) (string, error) {
	return s.StartSession(ctx, userID, Client{})
}

// StartSession signs a user in from a client and returns the cookie's token.
func (s *Service) StartSession(ctx context.Context, userID string, c Client) (string, error) {
	return s.StartSessionFor(ctx, auth.User{ID: userID}, c)
}

// StartSessionFor checks that the address the caller authenticated is still
// the account's address.
func (s *Service) StartSessionFor(ctx context.Context, u auth.User, c Client) (string, error) {
	token, ok, err := s.newSession(ctx, u, "", c)
	if err == nil && !ok {
		err = ErrBadLogin
	}
	return token, err
}

// newSession checks the account and any non-empty email and password hash
// in the same statement that inserts the session.
func (s *Service) newSession(ctx context.Context, u auth.User, password string, c Client) (token string, ok bool, err error) {
	b := make([]byte, 32)
	rand.Read(b)
	token = base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ? OR last_seen_at < ?`, now.Unix(), now.Add(-SessionIdle).Unix())
	res, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at, authenticated_at, created_at, last_seen_at, user_agent, ip)
		SELECT ?, id, ?, ?, ?, ?, ?, ? FROM users
		WHERE id = ? AND (? = '' OR email = ?) AND (? = '' OR password = ?)`,
		sessionID(token), now.Add(SessionTTL).Unix(), now.Unix(), now.Unix(), now.Unix(), c.UserAgent, c.IP,
		u.ID, u.Email, u.Email, password, password)
	if err != nil {
		return "", false, err
	}
	n, err := res.RowsAffected()
	if n != 1 || err != nil {
		return "", false, err
	}
	return token, true, nil
}

// Session returns the user a session token belongs to, if the session is
// neither past its lifetime nor idle too long.
func (s *Service) Session(ctx context.Context, token string) (auth.User, error) {
	u, _, err := s.session(ctx, token)
	return u, err
}

func (s *Service) session(ctx context.Context, token string) (u auth.User, lastSeen int64, err error) {
	now := s.now()
	err = s.DB.QueryRowContext(ctx, `SELECT u.id, u.email, s.last_seen_at FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = ? AND s.expires_at > ? AND s.last_seen_at > ?`,
		sessionID(token), now.Unix(), now.Add(-SessionIdle).Unix()).Scan(&u.ID, &u.Email, &lastSeen)
	return u, lastSeen, err
}

// seen records that a session was used now, from c, unless that was already
// recorded within the last hour. It checks again that the session is still
// valid, so a request held up past the idle timeout can't revive it.
func (s *Service) seen(ctx context.Context, token string, lastSeen int64, c Client) error {
	now := s.now()
	if now.Unix()-lastSeen < int64(seenEvery/time.Second) {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, ip = ?, user_agent = ?
		WHERE id = ? AND last_seen_at <= ? AND last_seen_at > ? AND expires_at > ?`,
		now.Unix(), c.IP, c.UserAgent, sessionID(token), now.Add(-seenEvery).Unix(), now.Add(-SessionIdle).Unix(), now.Unix())
	return err
}

func (s *Service) EndSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID(token))
	return err
}

// Sessions lists a user's sessions that are still valid: the caller's
// (current, from CurrentSession) first, then the most recently used.
func (s *Service) Sessions(ctx context.Context, userID, current string) ([]SessionInfo, error) {
	now := s.now()
	rows, err := s.DB.QueryContext(ctx, `SELECT id, created_at, last_seen_at, user_agent, ip FROM sessions
		WHERE user_id = ? AND expires_at > ? AND last_seen_at > ? ORDER BY id = ? DESC, last_seen_at DESC, created_at DESC`,
		userID, now.Unix(), now.Add(-SessionIdle).Unix(), current)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SessionInfo{}
	for rows.Next() {
		var si SessionInfo
		var created, seen int64
		if err := rows.Scan(&si.ID, &created, &seen, &si.UserAgent, &si.IP); err != nil {
			return nil, err
		}
		si.CreatedAt, si.LastSeenAt, si.Current = time.Unix(created, 0), time.Unix(seen, 0), si.ID == current
		out = append(out, si)
	}
	return out, rows.Err()
}

// RevokeSession ends one of a user's sessions: ErrNoSession if it isn't theirs.
func (s *Service) RevokeSession(ctx context.Context, userID, id string) error {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoSession
	}
	return nil
}

// RevokeOtherSessions ends every session of a user except keep, and says how
// many it ended.
func (s *Service) RevokeOtherSessions(ctx context.Context, userID, keep string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keep)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

type sessionKey struct{}

// CurrentSession is the id of the session the request came with ("" without one).
func CurrentSession(ctx context.Context) string {
	id, _ := ctx.Value(sessionKey{}).(string)
	return id
}

// SessionToken is the token of the session cookie a request goes by. Over
// https that is the __Host- cookie; the plain name counts only when there is
// none, for sessions from before it. Never both: if the __Host- session ends,
// the browser is signed out rather than silently becoming whoever the other
// cookie belongs to.
func SessionToken(r *http.Request) (string, bool) {
	names := []string{SessionCookie}
	if Secure(r) {
		names = []string{SecureSessionCookie, SessionCookie}
	}
	for _, name := range names {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			return c.Value, true
		}
	}
	return "", false
}

// SessionTokens are every session token a request carries, under either
// name: signing out ends them all.
func SessionTokens(r *http.Request) []string {
	var out []string
	for _, name := range []string{SecureSessionCookie, SessionCookie} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			out = append(out, c.Value)
		}
	}
	return out
}

// SessionFrom is the user and token of the request's session (SessionToken);
// sql.ErrNoRows when it has none or it isn't valid.
func (s *Service) SessionFrom(ctx context.Context, r *http.Request) (auth.User, string, error) {
	u, token, _, err := s.sessionFrom(ctx, r)
	return u, token, err
}

func (s *Service) sessionFrom(ctx context.Context, r *http.Request) (auth.User, string, int64, error) {
	token, ok := SessionToken(r)
	if !ok {
		return auth.User{}, "", 0, sql.ErrNoRows
	}
	u, lastSeen, err := s.session(ctx, token)
	if err != nil {
		return auth.User{}, "", 0, err
	}
	return u, token, lastSeen, nil
}

// SessionGuard is the web UI guard for REFRACT_AUTH=builtin. Requests with a
// valid session go on as that user. Without one, /api gets 401 and every
// other path is served by public: the app's files, which show the sign-in page.
func (s *Service) SessionGuard(public http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if u, token, lastSeen, err := s.sessionFrom(r.Context(), r); err == nil {
				// A failed write only delays the record; the request goes on.
				s.seen(r.Context(), token, lastSeen, ClientOf(r))
				r = r.WithContext(context.WithValue(r.Context(), sessionKey{}, sessionID(token)))
				next.ServeHTTP(w, auth.WithIdentity(r, u.Email))
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":{"kind":"login_required","message":"Sign in to continue."}}`))
				return
			}
			public.ServeHTTP(w, r)
		})
	}
}

// SetSessionCookie writes the session cookie, under the __Host- name over
// https (directly or through a proxy), and drops a plain-named one left from
// before.
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	secure := Secure(r)
	name := SessionCookie
	if secure {
		name = SecureSessionCookie
		if _, err := r.Cookie(SessionCookie); err == nil {
			clearCookie(w, SessionCookie, true)
		}
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: token, Path: "/", MaxAge: int(SessionTTL.Seconds()),
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}

// ClearSessionCookie removes the session cookie under either name.
func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	secure := Secure(r)
	clearCookie(w, SessionCookie, secure)
	if secure {
		clearCookie(w, SecureSessionCookie, true)
	}
}

func clearCookie(w http.ResponseWriter, name string, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: name, Path: "/", MaxAge: -1, HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode})
}
