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
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

	maxFailures      = 10 // per account
	maxFailuresPerIP = 30 // per address: allows a few people behind one network
	failWindow       = 15 * time.Minute
	maxTracked       = 100_000

	maxMailsPerEmail = 3  // emails asked for to one address per window
	maxMailsPerIP    = 20 // and from one network address
	maxResetsPerIP   = 20 // password resets tried per window from one network address
)

var (
	ErrBadLogin = errors.New("wrong email or password")
	ErrTooMany  = errors.New("too many failed attempts: try again in a few minutes")
)

// LowerHashCostForTests makes password hashing fast, for the tests of packages
// that sign people in: the real count makes each hash take about 0.3 s.
func LowerHashCostForTests() { pbkdf2Iter = 1000 }

// hashSlots bounds how many password hashes run at once, server-wide, so a
// flood of sign-ups, resets or password changes can't take every CPU.
var hashSlots = make(chan struct{}, max(1, runtime.NumCPU()))

// hashRuns counts hashes made, for tests that check no work is done for a
// request that is refused first.
var hashRuns atomic.Int64

func hashPassword(pw string) (string, error) {
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
	hashRuns.Add(1)
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
	hashSlots <- struct{}{}
	defer func() { <-hashSlots }()
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
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return u, err
	}
	defer tx.Rollback()
	if err := putPassword(ctx, tx, u.ID, h); err != nil {
		return u, err
	}
	return u, tx.Commit()
}

func putPassword(ctx context.Context, tx *sql.Tx, id, hash string) error {
	res, err := tx.ExecContext(ctx, `UPDATE users SET password = ? WHERE id = ?`, hash, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		return cmp.Or(err, ErrNoAccount)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	// A link to choose a password is no longer wanted once one was chosen.
	_, err = tx.ExecContext(ctx, `DELETE FROM account_tokens WHERE user_id = ? AND purpose = ?`, id, Reset)
	return err
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
	u, _, err := s.login(ctx, email, pw, ip)
	return u, err
}

// SignIn is Login followed by a new session, whose token it returns. The
// session is only created if the password that was checked is still the
// account's: checking takes a while, and meanwhile the address's real owner
// may have taken over an unverified account (ConfirmByProvider, a reset) or
// the password may have changed. Then it is ErrBadLogin.
func (s *Service) SignIn(ctx context.Context, email, pw string, c Client) (auth.User, string, error) {
	u, stored, err := s.login(ctx, email, pw, c.IP)
	if err != nil {
		return auth.User{}, "", err
	}
	if testHookPasswordChecked != nil {
		testHookPasswordChecked(u.ID)
	}
	token, ok, err := s.newSession(ctx, u, stored, c)
	if err != nil {
		return auth.User{}, "", err
	}
	if !ok {
		return auth.User{}, "", ErrBadLogin
	}
	return u, token, nil
}

// testHookPasswordChecked, when set, runs in SignIn between the password check
// and the session: tests use it to change the account in that window.
var testHookPasswordChecked func(userID string)

// login is Login, also returning the stored hash that the password matched.
func (s *Service) login(ctx context.Context, email, pw, ip string) (auth.User, string, error) {
	email = normEmail(email)
	keys := []string{"email:" + email}
	if ip != "" {
		keys = append(keys, "ip:"+ip)
	}
	if ok, err := s.allowAttempt(ctx, keys); err != nil || !ok {
		return auth.User{}, "", cmp.Or(err, ErrTooMany)
	}
	u := auth.User{Email: email}
	var stored string
	err := s.DB.QueryRowContext(ctx, `SELECT id, password FROM users WHERE email = ?`, email).Scan(&u.ID, &stored)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return auth.User{}, "", err
	}
	if stored == "" {
		checkPassword(dummyHash(), pw)
		return auth.User{}, "", cmp.Or(s.count(ctx, keys), ErrBadLogin)
	}
	if !checkPassword(stored, pw) {
		return auth.User{}, "", cmp.Or(s.count(ctx, keys), ErrBadLogin)
	}
	// The address keeps its count: it may be trying several accounts.
	_, err = s.DB.ExecContext(ctx, `DELETE FROM login_failures WHERE key = ?`, keys[0])
	return u, stored, err
}

// limits: failures allowed per window, by kind of key.
var limits = map[string]int{"email": maxFailures, "ip": maxFailuresPerIP, "mail": maxMailsPerEmail, "mailip": maxMailsPerIP, "reset": maxResetsPerIP}

// ErrTooManyMails is AllowMail's refusal.
var ErrTooManyMails = errors.New("too many emails requested from this network address: try again in a few minutes")

// AllowMail counts one email to address, asked for from ip. Past the network
// address's limit it refuses with ErrTooManyMails: that says nothing about the
// address. Past the address's own limit it answers send=false with no error:
// callers then answer exactly as if they had sent, so the limit neither tells
// whether the address has an account nor tells whoever is flooding it. It runs
// before any account lookup.
func (s *Service) AllowMail(ctx context.Context, email, ip string) (send bool, err error) {
	// Check and count are one step: concurrent requests can't all pass a
	// check made before any of them counted.
	limitMu.Lock()
	defer limitMu.Unlock()
	var keys []string
	if ip != "" {
		keys = append(keys, "mailip:"+ip)
		if ok, err := s.allowAttempt(ctx, keys); err != nil || !ok {
			return false, cmp.Or(err, ErrTooManyMails)
		}
	}
	mailKey := "mail:" + normEmail(email)
	ok, err := s.allowAttempt(ctx, []string{mailKey})
	if err != nil {
		return false, err
	}
	if ok {
		keys = append(keys, mailKey)
	}
	return ok, s.count(ctx, keys)
}

// limitMu makes "check the limit, then count" one step for the limits that
// admit work (emails, resets). Counters live in the database, but this
// process is the only one that adds to them.
var limitMu sync.Mutex

// ErrTooManyResets is AllowReset's refusal.
var ErrTooManyResets = errors.New("too many attempts from this network address: try again in a few minutes")

// AllowReset counts one attempt to redeem a reset link from ip, refusing with
// ErrTooManyResets past the limit. An empty ip is not limited.
func (s *Service) AllowReset(ctx context.Context, ip string) error {
	if ip == "" {
		return nil
	}
	limitMu.Lock()
	defer limitMu.Unlock()
	keys := []string{"reset:" + ip}
	if ok, err := s.allowAttempt(ctx, keys); err != nil || !ok {
		return cmp.Or(err, ErrTooManyResets)
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
	acct, err := s.Lookup(ctx, email)
	if errors.Is(err, ErrNoAccount) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return s.SignOutAccount(ctx, acct.ID)
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

// OwnedCount is how many projects an account owns, live or in the trash.
func (s *Service) OwnedCount(ctx context.Context, userID string) (n int, err error) {
	err = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM projects WHERE owner = ?`, userID).Scan(&n)
	return n, err
}

// Remove deletes an account and everything it owns, and returns how many
// projects went. destroy reserves and deletes the projects, live or in the
// trash. If deletion fails part way, the account remains and the count says
// how many projects went. Then it removes the account as Delete does,
// refusing with ErrOwnsProjects if a project appeared meanwhile.
func (s *Service) Remove(ctx context.Context, userID string, destroy func(context.Context, []string) (int, error)) (int, error) {
	acct, err := s.AccountByID(ctx, userID)
	if err != nil {
		return 0, err
	}
	owned, err := s.OwnedProjects(ctx, userID)
	if err != nil {
		return 0, err
	}
	ids := make([]string, 0, len(owned))
	for _, p := range owned {
		ids = append(ids, p.ID)
	}
	deleted, err := destroy(ctx, ids)
	if err != nil {
		return deleted, err
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return deleted, err
	}
	defer tx.Rollback()
	if err := deleteAccount(ctx, tx, userID, acct.Email); err != nil {
		return deleted, err
	}
	return deleted, tx.Commit()
}

// Exists reports whether a user with this email exists.
func (s *Service) Exists(ctx context.Context, email string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE email = ?`, normEmail(email)).Scan(&n)
	return n > 0, err
}

// List returns every user's email, and whether they have a password.
func (s *Service) List(ctx context.Context) (map[string]bool, error) {
	accounts, err := s.AdminAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(accounts))
	for _, a := range accounts {
		out[a.Email] = a.HasPassword
	}
	return out, nil
}

// sessionID is what the database keeps: a leaked database holds no usable cookies.
func sessionID(token string) string { return digest(token) }

// digest is the sha256 (hex) of a token: what the database keeps of cookies and links.
func digest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func Secure(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}
