package users

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ponytojas/Refract/internal/auth"
)

func TestSessionGuardDoesNotReviveRevokedAccount(t *testing.T) {
	for _, action := range []string{"rename", "delete"} {
		t.Run(action, func(t *testing.T) {
			s, ctx := newService(t), t.Context()
			const email = "ana@example.com"
			u, err := s.Resolve(ctx, email)
			if err != nil {
				t.Fatal(err)
			}
			token, err := s.NewSession(ctx, u.ID)
			if err != nil {
				t.Fatal(err)
			}
			protected := s.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Error("revoked session reached the protected handler")
			}))
			h := s.SessionGuard(http.NotFoundHandler())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var err error
				if action == "rename" {
					err = s.RenameAccount(ctx, u.ID, "renamed@example.com")
				} else {
					err = s.Delete(ctx, email)
				}
				if err != nil {
					t.Fatal(err)
				}
				protected.ServeHTTP(w, r)
			}))
			r := httptest.NewRequest("GET", "/api/me", nil)
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusForbidden {
				t.Errorf("revoked session received status %d", w.Code)
			}
			if _, err := s.Lookup(ctx, email); !errors.Is(err, ErrNoAccount) {
				t.Errorf("old address still exists: %v", err)
			}
		})
	}
}

// clock is a settable time for a Service.
type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newClockedService(t *testing.T) (*Service, *clock) {
	s := newService(t)
	c := &clock{time.Unix(1_800_000_000, 0)}
	s.Now = c.now
	return s, c
}

func TestSessionIdleAndAbsoluteExpiry(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")

	idle, _ := s.NewSession(ctx, u.ID)
	c.advance(SessionIdle - time.Minute)
	if _, err := s.Session(ctx, idle); err != nil {
		t.Fatalf("expired before the idle timeout: %v", err)
	}
	c.advance(2 * time.Minute)
	if _, err := s.Session(ctx, idle); err == nil {
		t.Fatal("an idle session is still valid")
	}

	// Used every few days, a session still ends 30 days after it began.
	used, _ := s.NewSession(ctx, u.ID)
	for i := 0; i < 9; i++ {
		c.advance(3 * 24 * time.Hour)
		_, last, err := s.session(ctx, used)
		if err != nil {
			t.Fatalf("day %d: %v", 3*(i+1), err)
		}
		s.seen(ctx, used, last, Client{})
	}
	c.advance(3 * 24 * time.Hour)
	if _, err := s.Session(ctx, used); err == nil {
		t.Fatal("a session outlived its 30 days")
	}
}

func TestSessionSeenIsThrottled(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, _ := s.StartSession(ctx, u.ID, Client{IP: "192.0.2.1", UserAgent: "first"})
	start := c.t.Unix()
	stored := func() (seen int64, ip, ua string) {
		s.DB.QueryRow(`SELECT last_seen_at, ip, user_agent FROM sessions WHERE id = ?`, sessionID(token)).Scan(&seen, &ip, &ua)
		return
	}
	guard := s.SessionGuard(http.NotFoundHandler())(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	hit := func(ua string) {
		r := httptest.NewRequest("GET", "/api/x", nil)
		r.RemoteAddr = "198.51.100.7:1234"
		r.Header.Set("User-Agent", ua)
		r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
		w := httptest.NewRecorder()
		guard.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("guard: %d", w.Code)
		}
	}

	c.advance(59 * time.Minute)
	hit("second")
	if seen, ip, ua := stored(); seen != start || ip != "192.0.2.1" || ua != "first" {
		t.Fatalf("written within the hour: %d %s %s", seen, ip, ua)
	}
	c.advance(time.Minute)
	hit("third")
	if seen, ip, ua := stored(); seen != c.t.Unix() || ip != "198.51.100.7" || ua != "third" {
		t.Fatalf("not written after an hour: %d %s %s", seen, ip, ua)
	}
}

func TestListAndRevokeSessions(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	other, _ := s.SetPassword(ctx, "bob@example.com", "correct horse")
	a, _ := s.StartSession(ctx, u.ID, Client{IP: "192.0.2.1", UserAgent: "laptop"})
	c.advance(time.Hour)
	b, _ := s.StartSession(ctx, u.ID, Client{IP: "192.0.2.2", UserAgent: "phone"})
	bobs, _ := s.NewSession(ctx, other.ID)

	list, err := s.Sessions(ctx, u.ID, sessionID(a))
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v %+v", err, list)
	}
	if list[0].UserAgent != "laptop" || !list[0].Current || list[0].IP != "192.0.2.1" || list[1].UserAgent != "phone" || list[1].Current {
		t.Fatalf("list = %+v", list)
	}
	if list, _ := s.Sessions(ctx, u.ID, ""); list[0].UserAgent != "phone" {
		t.Fatalf("not most recent first: %+v", list)
	}

	if err := s.RevokeSession(ctx, u.ID, sessionID(bobs)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("revoked another account's session: %v", err)
	}
	if _, err := s.Session(ctx, bobs); err != nil {
		t.Fatal("another account's session ended")
	}
	if err := s.RevokeSession(ctx, u.ID, sessionID(b)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, b); err == nil {
		t.Fatal("a revoked session is still valid")
	}

	b, _ = s.NewSession(ctx, u.ID)
	d, _ := s.NewSession(ctx, u.ID)
	if n, err := s.RevokeOtherSessions(ctx, u.ID, sessionID(a)); err != nil || n != 2 {
		t.Fatalf("revoke others: %d %v", n, err)
	}
	for _, tok := range []string{b, d} {
		if _, err := s.Session(ctx, tok); err == nil {
			t.Fatal("another session survived")
		}
	}
	for _, tok := range []string{a, bobs} {
		if _, err := s.Session(ctx, tok); err != nil {
			t.Fatal("revoking others ended the wrong session")
		}
	}

	// Idle sessions are not listed, and go when the next one is made.
	c.advance(SessionIdle + time.Minute)
	if list, _ := s.Sessions(ctx, u.ID, ""); len(list) != 0 {
		t.Fatalf("idle sessions listed: %+v", list)
	}
	s.NewSession(ctx, other.ID)
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, u.ID).Scan(&n)
	if n != 0 {
		t.Fatalf("%d idle sessions kept", n)
	}
}

func TestClientOfKeepsUserAgentValid(t *testing.T) {
	for _, ua := range []string{
		"a" + strings.Repeat("é", 200), // the cut splits a character
		"Mozilla/5.0 \xff\xfe broken",  // short and invalid
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("User-Agent", ua)
		got := ClientOf(r).UserAgent
		if len(got) > maxUserAgent || !utf8.ValidString(got) || got == "" {
			t.Fatalf("user agent %d bytes, valid %v: %q", len(got), utf8.ValidString(got), got)
		}
	}
}

func TestSessionCookieNames(t *testing.T) {
	plain := httptest.NewRequest("GET", "http://localhost/", nil)
	secure := httptest.NewRequest("GET", "http://r.test/", nil)
	secure.Header.Set("X-Forwarded-Proto", "https")

	w := httptest.NewRecorder()
	SetSessionCookie(w, plain, "tok")
	if cs := w.Result().Cookies(); len(cs) != 1 || cs[0].Name != SessionCookie || cs[0].Secure {
		t.Fatalf("over http: %+v", cs)
	}

	secure.AddCookie(&http.Cookie{Name: SessionCookie, Value: "old"})
	w = httptest.NewRecorder()
	SetSessionCookie(w, secure, "tok")
	var set, dropped bool
	for _, c := range w.Result().Cookies() {
		set = set || (c.Name == SecureSessionCookie && c.Value == "tok" && c.Secure && c.Path == "/" && c.Domain == "")
		dropped = dropped || (c.Name == SessionCookie && c.MaxAge < 0)
	}
	if !set || !dropped {
		t.Fatalf("over https: %+v", w.Result().Cookies())
	}

	// Over https the legacy name is read only without a __Host- cookie.
	if tok, ok := SessionToken(secure); !ok || tok != "old" {
		t.Fatalf("legacy cookie: %q %v", tok, ok)
	}
	secure.AddCookie(&http.Cookie{Name: SecureSessionCookie, Value: "new"})
	if tok, _ := SessionToken(secure); tok != "new" {
		t.Fatalf("got %q", tok)
	}
	if got := SessionTokens(secure); len(got) != 2 {
		t.Fatalf("signing out would miss one of %q", got)
	}
	// Over http a __Host- cookie can't exist, and isn't read.
	plain.AddCookie(&http.Cookie{Name: SecureSessionCookie, Value: "new"})
	if _, ok := SessionToken(plain); ok {
		t.Fatal("read a __Host- cookie over http")
	}

	w = httptest.NewRecorder()
	ClearSessionCookie(w, secure)
	if cs := w.Result().Cookies(); len(cs) != 2 {
		t.Fatalf("clear over https: %+v", cs)
	}
}

// With both cookie names, the __Host- one decides: when its session ends the
// browser is signed out, not switched to the other cookie's session.
func TestSessionFromNeverFallsBack(t *testing.T) {
	s, ctx := newService(t), context.Background()
	ana, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	bob, _ := s.SetPassword(ctx, "bob@example.com", "correct horse")
	anas, _ := s.NewSession(ctx, ana.ID)
	bobs, _ := s.NewSession(ctx, bob.ID)
	r := httptest.NewRequest("GET", "http://r.test/api/x", nil)
	r.Header.Set("X-Forwarded-Proto", "https")
	r.AddCookie(&http.Cookie{Name: SecureSessionCookie, Value: anas})
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: bobs})
	if got, _, err := s.SessionFrom(ctx, r); err != nil || got.ID != ana.ID {
		t.Fatalf("got %+v %v", got, err)
	}
	s.EndSession(ctx, anas)
	if got, _, err := s.SessionFrom(ctx, r); err == nil {
		t.Fatalf("switched to %s's session", got.Email)
	}
	// Without a __Host- cookie, a session from before it still works.
	legacy := httptest.NewRequest("GET", "http://r.test/api/x", nil)
	legacy.Header.Set("X-Forwarded-Proto", "https")
	legacy.AddCookie(&http.Cookie{Name: SessionCookie, Value: bobs})
	if got, _, err := s.SessionFrom(ctx, legacy); err != nil || got.ID != bob.ID {
		t.Fatalf("legacy cookie: %+v %v", got, err)
	}
}

// A request that checked a session just before the idle timeout and records
// its use just after doesn't revive it; nor does one past the 30 days.
func TestSeenDoesNotReviveAnEndedSession(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, _ := s.NewSession(ctx, u.ID)
	c.advance(SessionIdle - time.Second)
	_, last, err := s.session(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	c.advance(2 * time.Second)
	if err := s.seen(ctx, token, last, Client{}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Session(ctx, token); err == nil {
		t.Fatal("an idle session came back")
	}

	// Past its 30 days, used two hours ago: the write must not happen.
	token, _ = s.NewSession(ctx, u.ID)
	before := c.t.Add(-2 * time.Hour).Unix()
	s.DB.Exec(`UPDATE sessions SET expires_at = ?, last_seen_at = ? WHERE id = ?`, c.t.Unix(), before, sessionID(token))
	s.seen(ctx, token, before, Client{})
	var seen int64
	s.DB.QueryRow(`SELECT last_seen_at FROM sessions WHERE id = ?`, sessionID(token)).Scan(&seen)
	if seen != before {
		t.Fatalf("an expired session's use was recorded: %d", seen)
	}
}

func TestAuthenticatedAtIgnoresIdleSessions(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, _ := s.NewSession(ctx, u.ID)
	if at, err := s.AuthenticatedAt(ctx, token); err != nil || at.IsZero() {
		t.Fatalf("fresh session: %v %v", at, err)
	}
	c.advance(SessionIdle + time.Minute)
	if at, err := s.AuthenticatedAt(ctx, token); err == nil && !at.IsZero() {
		t.Fatalf("an idle session reports a sign-in at %v", at)
	}
}

// Many requests that find last_seen_at stale at once write it once: the
// conditional UPDATE lets only the first through.
func TestSeenWritesOnceUnderConcurrency(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, _ := s.NewSession(ctx, u.ID)
	if _, err := s.DB.Exec(`CREATE TABLE seen_log (at INTEGER);
		CREATE TRIGGER seen_logged AFTER UPDATE OF last_seen_at ON sessions BEGIN INSERT INTO seen_log VALUES (NEW.last_seen_at); END`); err != nil {
		t.Fatal(err)
	}
	c.advance(2 * time.Hour)
	_, last, err := s.session(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.seen(ctx, token, last, Client{UserAgent: "x"})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM seen_log`).Scan(&n)
	if n != 1 {
		t.Fatalf("%d writes, want 1", n)
	}
}

// When recording a session's use fails, the request still goes through.
func TestGuardServesWhenSeenFails(t *testing.T) {
	s, c := newClockedService(t)
	ctx := context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, _ := s.NewSession(ctx, u.ID)
	if _, err := s.DB.Exec(`CREATE TRIGGER no_seen BEFORE UPDATE OF last_seen_at ON sessions BEGIN SELECT RAISE(ABORT, 'read-only'); END`); err != nil {
		t.Fatal(err)
	}
	c.advance(2 * time.Hour)
	_, last, _ := s.session(ctx, token)
	if err := s.seen(ctx, token, last, Client{}); err == nil {
		t.Fatal("the trigger didn't make the write fail")
	}
	h := s.SessionGuard(http.NotFoundHandler())(s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := auth.UserFrom(r.Context())
		w.Write([]byte(got.Email))
	})))
	r := httptest.NewRequest("GET", "/api/x", nil)
	r.AddCookie(&http.Cookie{Name: SessionCookie, Value: token})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.String() != "ana@example.com" {
		t.Fatalf("got %d %q", w.Code, w.Body.String())
	}
}
