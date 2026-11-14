package users

import (
	"cmp"
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

// Passwords and sessions, for REFRACT_AUTH=builtin.

// pbkdf2Iter is OWASP's figure for PBKDF2-HMAC-SHA256. A variable only so tests
// can hash quickly; stored hashes carry their own count.
var pbkdf2Iter = 600_000

const (
	MinPassword = 8
	maxPassword = 1024

	SessionCookie = "refract_session"
	SessionTTL    = 30 * 24 * time.Hour

	maxFailures      = 10 // per account
	maxFailuresPerIP = 30 // per address: allows a few people behind one network
	failWindow       = 15 * time.Minute
	maxTracked       = 100_000

	maxMailsPerEmail = 3  // emails asked for to one address per window
	maxMailsPerIP    = 20 // and from one network address
)

var (
	ErrBadLogin = errors.New("wrong email or password")
	ErrTooMany  = errors.New("too many failed attempts: try again in a few minutes")
)

// LowerHashCostForTests makes password hashing fast, for the tests of packages
// that sign people in: the real count makes each hash take about 0.3 s.
func LowerHashCostForTests() { pbkdf2Iter = 1000 }

func hashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	k, err := pbkdf2.Key(sha256.New, pw, salt, pbkdf2Iter, 32)
	if err != nil {
		return "", err
	}
	b64 := base64.RawStdEncoding.EncodeToString
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", pbkdf2Iter, b64(salt), b64(k)), nil
}

func checkPassword(stored, pw string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err1 := strconv.Atoi(parts[1])
	salt, err2 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err3 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil || err3 != nil || iter < 1 {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, pw, salt, iter, len(want))
	return err == nil && subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is checked against when the email is unknown, so a wrong email
// takes as long as a wrong password.
var dummyHash = sync.OnceValue(func() string { h, _ := hashPassword("refract"); return h })

func normEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// SetPassword sets a user's password, creating the user if needed, and signs
// them out everywhere.
func (s *Service) SetPassword(ctx context.Context, email, pw string) (auth.User, error) {
	email = normEmail(email)
	if err := validEmail(email); err != nil {
		return auth.User{}, err
	}
	if err := validPassword(pw); err != nil {
		return auth.User{}, err
	}
	u, err := s.Resolve(ctx, email)
	if err != nil {
		return auth.User{}, err
	}
	h, err := hashPassword(pw)
	if err != nil {
		return auth.User{}, err
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE users SET password = ? WHERE id = ?`, h, u.ID); err != nil {
		return auth.User{}, err
	}
	if _, err = s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, u.ID); err != nil {
		return u, err
	}
	// A link to choose a password is no longer wanted once one was chosen.
	_, err = s.DB.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ? AND purpose = ?`, u.ID, Reset)
	return u, err
}

// validEmail checks an already normalized address.
func validEmail(email string) error {
	if !strings.Contains(email, "@") || strings.ContainsAny(email, " \t\r\n") {
		return fmt.Errorf("%q is not an email address", email)
	}
	return nil
}

// ValidatePassword checks the length rules of a new password.
func ValidatePassword(pw string) error { return validPassword(pw) }

func validPassword(pw string) error {
	if len(pw) < MinPassword || len(pw) > maxPassword {
		return fmt.Errorf("passwords need %d to %d characters", MinPassword, maxPassword)
	}
	return nil
}

// Login checks an email and password. ip is the client's address (ClientIP).
// Failures are counted per account and per address: guessing one account's
// password, or many accounts from one place, is refused for a while. The
// counts live in the database, so a restart doesn't reset them and
// `refract users unlock` (another process) can.
func (s *Service) Login(ctx context.Context, email, pw, ip string) (auth.User, error) {
	email = normEmail(email)
	keys := []string{"email:" + email}
	if ip != "" {
		keys = append(keys, "ip:"+ip)
	}
	if ok, err := s.allowAttempt(ctx, keys); err != nil || !ok {
		return auth.User{}, cmp.Or(err, ErrTooMany)
	}
	u := auth.User{Email: email}
	var stored string
	err := s.DB.QueryRowContext(ctx, `SELECT id, password FROM users WHERE email = ?`, email).Scan(&u.ID, &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, err
	}
	if stored == "" {
		checkPassword(dummyHash(), pw)
		return auth.User{}, cmp.Or(s.count(ctx, keys), ErrBadLogin)
	}
	if !checkPassword(stored, pw) {
		return auth.User{}, cmp.Or(s.count(ctx, keys), ErrBadLogin)
	}
	// The address keeps its count: it may be trying several accounts.
	_, err = s.DB.ExecContext(ctx, `DELETE FROM login_failures WHERE key = ?`, keys[0])
	return u, err
}

// limits: failures allowed per window, by kind of key.
var limits = map[string]int{"email": maxFailures, "ip": maxFailuresPerIP, "mail": maxMailsPerEmail, "mailip": maxMailsPerIP}

// ErrTooManyMails is AllowMail's refusal.
var ErrTooManyMails = errors.New("too many emails to this address: try again in a few minutes")

// AllowMail counts one email to address, asked for from ip, and refuses past
// the limits. It runs before any account lookup, so its answer is the same
// whether or not the address has an account.
func (s *Service) AllowMail(ctx context.Context, email, ip string) error {
	keys := []string{"mail:" + normEmail(email)}
	if ip != "" {
		keys = append(keys, "mailip:"+ip)
	}
	if ok, err := s.allowAttempt(ctx, keys); err != nil || !ok {
		return cmp.Or(err, ErrTooManyMails)
	}
	return s.count(ctx, keys)
}

func (s *Service) allowAttempt(ctx context.Context, keys []string) (bool, error) {
	expired := s.now().Add(-failWindow).Unix()
	for _, k := range keys {
		var n int
		err := s.DB.QueryRowContext(ctx, `SELECT count FROM login_failures WHERE key = ? AND since > ?`, k, expired).Scan(&n)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return false, err
		}
		kind, _, _ := strings.Cut(k, ":")
		if n >= limits[kind] {
			return false, nil
		}
	}
	return true, nil
}

// count adds one to each key: a failed sign-in, or an email asked for.
func (s *Service) count(ctx context.Context, keys []string) error {
	now := s.now()
	// Made-up emails and addresses each add a row: expired rows go first, and
	// while the table is full of recent ones, only existing keys are counted.
	if _, err := s.DB.ExecContext(ctx, `DELETE FROM login_failures WHERE since <= ?`, now.Add(-failWindow).Unix()); err != nil {
		return err
	}
	var rows int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM login_failures`).Scan(&rows); err != nil {
		return err
	}
	for _, k := range keys {
		var err error
		if rows < maxTracked {
			_, err = s.DB.ExecContext(ctx, `INSERT INTO login_failures (key, count, since) VALUES (?, 1, ?)
				ON CONFLICT (key) DO UPDATE SET count = count + 1`, k, now.Unix())
		} else {
			_, err = s.DB.ExecContext(ctx, `UPDATE login_failures SET count = count + 1 WHERE key = ?`, k)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Unlock clears an account's failed sign-ins, so it can try again at once.
// An address that failed too often still waits out its window.
func (s *Service) Unlock(ctx context.Context, email string) (bool, error) {
	e := normEmail(email)
	res, err := s.DB.ExecContext(ctx, `DELETE FROM login_failures WHERE key IN (?, ?)`, "email:"+e, "mail:"+e)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UnlockAll clears every failed sign-in, of accounts and addresses.
func (s *Service) UnlockAll(ctx context.Context) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM login_failures`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ClientIP is the address a request came from. Behind a reverse proxy on the
// same machine or private network, that is the last address the proxy added
// to X-Forwarded-For; from anywhere else the header is ignored, since a
// client could forge it.
func ClientIP(r *http.Request) string {
	peer, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		peer = r.RemoteAddr
	}
	ip := net.ParseIP(peer)
	if ip == nil || !(ip.IsLoopback() || ip.IsPrivate()) {
		return peer
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	if last := net.ParseIP(strings.TrimSpace(hops[len(hops)-1])); last != nil {
		return last.String()
	}
	return peer
}

// SignOut ends every session of a user.
func (s *Service) SignOut(ctx context.Context, email string) (int64, error) {
	res, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = (SELECT id FROM users WHERE email = ?)`, normEmail(email))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ErrOwnsProjects is returned when an account can't be removed because it still has projects.
var ErrOwnsProjects = errors.New("the account still owns projects")

// Delete removes an account: its sessions, its settings (AI connections,
// GitHub), its links and the user. Projects must be deleted first (see Remove,
// and refract users remove).
func (s *Service) Delete(ctx context.Context, email string) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var id string
	if err := tx.QueryRowContext(ctx, `SELECT id FROM users WHERE email = ?`, normEmail(email)).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("there is no account %q", email)
	} else if err != nil {
		return err
	}
	if err := deleteAccount(ctx, tx, id, normEmail(email)); err != nil {
		return err
	}
	return tx.Commit()
}

func deleteAccount(ctx context.Context, tx *sql.Tx, id, email string) error {
	var owned int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE owner = ?`, id).Scan(&owned); err != nil {
		return err
	}
	if owned > 0 {
		return fmt.Errorf("%s still owns %d projects: %w", email, owned, ErrOwnsProjects)
	}
	prefix := store.UserKey(id, "") // all of the user's settings start with it
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`DELETE FROM sessions WHERE user_id = ?`, []any{id}},
		{`DELETE FROM settings WHERE substr(key, 1, length(?)) = ?`, []any{prefix, prefix}},
		{`DELETE FROM ai_usage WHERE user_id = ?`, []any{id}},
		{`DELETE FROM login_failures WHERE key IN (?, ?)`, []any{"email:" + email, "mail:" + email}},
		{`DELETE FROM users WHERE id = ?`, []any{id}}, // its links go with it
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return nil
}

// OwnedProject is a project an account owns, live or in the trash.
type OwnedProject struct {
	ID, Name string
	Trashed  bool
}

func (s *Service) OwnedProjects(ctx context.Context, userID string) ([]OwnedProject, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name, deleted_at > 0 FROM projects WHERE owner = ? ORDER BY name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OwnedProject
	for rows.Next() {
		var p OwnedProject
		if err := rows.Scan(&p.ID, &p.Name, &p.Trashed); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Remove deletes an account and everything it owns, and returns how many
// projects went. destroy deletes one project for good, live or in the trash,
// files included. It stops at the first project it can't delete, leaving the
// account and the rest in place. Then it removes the account as Delete does,
// refusing with ErrOwnsProjects if a project appeared meanwhile.
func (s *Service) Remove(ctx context.Context, userID string, destroy func(ctx context.Context, projectID string) error) (int, error) {
	acct, err := s.AccountByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	owned, err := s.OwnedProjects(ctx, userID)
	if err != nil {
		return 0, err
	}
	for i, p := range owned {
		if err := destroy(ctx, p.ID); err != nil {
			return i, fmt.Errorf("deleting project %s (%s): %w", p.Name, p.ID, err)
		}
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return len(owned), err
	}
	defer tx.Rollback()
	if err := deleteAccount(ctx, tx, userID, acct.Email); err != nil {
		return len(owned), err
	}
	return len(owned), tx.Commit()
}

// HasPassword reports whether the user can sign in with a password.
func (s *Service) HasPassword(ctx context.Context, userID string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE id = ? AND password != ''`, userID).Scan(&n)
	return n > 0, err
}

// Rename changes a user's email, keeping everything they own. Moving from
// REFRACT_AUTH=none to a sign-in mode is "rename local you@example.com".
func (s *Service) Rename(ctx context.Context, from, to string) error {
	from, to = normEmail(from), normEmail(to)
	if !strings.Contains(to, "@") || strings.ContainsAny(to, " \t\r\n") {
		return fmt.Errorf("%q is not an email address", to)
	}
	if taken, err := s.Exists(ctx, to); err != nil || taken {
		return cmp.Or(err, fmt.Errorf("%s already has an account", to))
	}
	res, err := s.DB.ExecContext(ctx, `UPDATE users SET email = ? WHERE email = ?`, to, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("there is no account %q", from)
	}
	return nil
}

// Exists reports whether a user with this email exists.
func (s *Service) Exists(ctx context.Context, email string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE email = ?`, normEmail(email)).Scan(&n)
	return n > 0, err
}

// List returns every user's email, and whether they have a password.
func (s *Service) List(ctx context.Context) (map[string]bool, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT email, password != '' FROM users ORDER BY email`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var email string
		var has bool
		if err := rows.Scan(&email, &has); err != nil {
			return nil, err
		}
		out[email] = has
	}
	return out, rows.Err()
}

// sessionID is what the database keeps: a leaked database holds no usable cookies.
func sessionID(token string) string { return digest(token) }

// digest is the sha256 (hex) of a token: what the database keeps of cookies and links.
func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewSession signs a user in and returns the cookie's token.
func (s *Service) NewSession(ctx context.Context, userID string) (string, error) {
	b := make([]byte, 32)
	rand.Read(b)
	token := base64.RawURLEncoding.EncodeToString(b)
	now := s.now()
	s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	_, err := s.DB.ExecContext(ctx, `INSERT INTO sessions (id, user_id, expires_at, authenticated_at) VALUES (?, ?, ?, ?)`, sessionID(token), userID, now.Add(SessionTTL).Unix(), now.Unix())
	return token, err
}

// Session returns the user a session token belongs to.
func (s *Service) Session(ctx context.Context, token string) (auth.User, error) {
	var u auth.User
	err := s.DB.QueryRowContext(ctx, `SELECT u.id, u.email FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.id = ? AND s.expires_at > ?`,
		sessionID(token), time.Now().Unix()).Scan(&u.ID, &u.Email)
	return u, err
}

func (s *Service) EndSession(ctx context.Context, token string) error {
	_, err := s.DB.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID(token))
	return err
}

// SessionGuard is the web UI guard for REFRACT_AUTH=builtin. Requests with a
// valid session go on as that user. Without one, /api gets 401 and every
// other path is served by public: the app's files, which show the sign-in page.
func (s *Service) SessionGuard(public http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie(SessionCookie); err == nil {
				if u, err := s.Session(r.Context(), c.Value); err == nil {
					next.ServeHTTP(w, auth.WithIdentity(r, u.Email))
					return
				}
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

// SetSessionCookie and ClearSessionCookie write the session cookie. Secure
// when the request came over https (directly or through a proxy).
func SetSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: token, Path: "/", MaxAge: int(SessionTTL.Seconds()),
		HttpOnly: true, Secure: Secure(r), SameSite: http.SameSiteLaxMode})
}

func ClearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Path: "/", MaxAge: -1, HttpOnly: true, Secure: Secure(r), SameSite: http.SameSiteLaxMode})
}

func Secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
