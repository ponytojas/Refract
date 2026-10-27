package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

func newService(t *testing.T) *Service {
	db, err := store.Open(filepath.Join(t.TempDir(), "u.db"))
	if err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, Key: make([]byte, 32)}
}

func TestPasswordLogin(t *testing.T) {
	s, ctx := newService(t), context.Background()
	if _, err := s.SetPassword(ctx, "ana@example.com", "short"); err == nil {
		t.Fatal("accepted a short password")
	}
	if _, err := s.SetPassword(ctx, "local", "long enough"); err == nil {
		t.Fatal("accepted a non-email account")
	}
	u, err := s.SetPassword(ctx, " Ana@Example.com ", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	s.DB.QueryRow(`SELECT password FROM users WHERE id = ?`, u.ID).Scan(&stored)
	if stored == "" || stored == "correct horse" {
		t.Fatalf("stored %q", stored)
	}
	if got, err := s.Login(ctx, "ANA@example.com", "correct horse", ""); err != nil || got.ID != u.ID {
		t.Fatalf("login: %v %+v", err, got)
	}
	for _, c := range [][2]string{{"ana@example.com", "wrong"}, {"nobody@example.com", "correct horse"}} {
		if _, err := s.Login(ctx, c[0], c[1], ""); !errors.Is(err, ErrBadLogin) {
			t.Fatalf("%v: got %v", c, err)
		}
	}
	if has, _ := s.HasPassword(ctx, u.ID); !has {
		t.Fatal("HasPassword")
	}
}

func TestLoginLimit(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "correct horse")
	for range maxFailures {
		s.Login(ctx, "ana@example.com", "wrong", "")
	}
	if _, err := s.Login(ctx, "ana@example.com", "correct horse", ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("got %v", err)
	}
	if _, err := s.Login(ctx, "bob@example.com", "x", ""); !errors.Is(err, ErrBadLogin) {
		t.Fatalf("other accounts are not limited: %v", err)
	}
}

func TestSessions(t *testing.T) {
	s, ctx := newService(t), context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, err := s.NewSession(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	s.DB.QueryRow(`SELECT count(*) FROM sessions WHERE id = ?`, token).Scan(&n)
	if n != 0 {
		t.Fatal("the token itself is stored")
	}
	if got, err := s.Session(ctx, token); err != nil || got.Email != "ana@example.com" {
		t.Fatalf("session: %v %+v", err, got)
	}
	s.DB.Exec(`UPDATE sessions SET expires_at = 1`)
	if _, err := s.Session(ctx, token); err == nil {
		t.Fatal("expired session accepted")
	}
	token, _ = s.NewSession(ctx, u.ID)
	other, _ := s.NewSession(ctx, u.ID)
	s.EndSession(ctx, token)
	if _, err := s.Session(ctx, token); err == nil {
		t.Fatal("ended session accepted")
	}
	s.SetPassword(ctx, "ana@example.com", "battery staple")
	if _, err := s.Session(ctx, other); err == nil {
		t.Fatal("a new password must sign out every session")
	}
}

func TestSessionGuard(t *testing.T) {
	s, ctx := newService(t), context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	token, _ := s.NewSession(ctx, u.ID)
	public := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("public")) })
	h := s.SessionGuard(public)(s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ := auth.UserFrom(r.Context())
		w.Write([]byte(got.Email))
	})))
	cases := []struct {
		path, cookie string
		code         int
		body         string
	}{
		{"/api/projects", "", http.StatusUnauthorized, ""},
		{"/api/projects", "forged", http.StatusUnauthorized, ""},
		{"/p/123", "", http.StatusOK, "public"},
		{"/api/projects", token, http.StatusOK, "ana@example.com"},
		{"/p/123", token, http.StatusOK, "ana@example.com"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", c.path, nil)
		if c.cookie != "" {
			r.AddCookie(&http.Cookie{Name: SessionCookie, Value: c.cookie})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.code || (c.body != "" && w.Body.String() != c.body) {
			t.Errorf("%s cookie=%q: %d %q", c.path, c.cookie, w.Code, w.Body.String())
		}
	}
}

func TestRenameKeepsData(t *testing.T) {
	s, ctx := newService(t), context.Background()
	local, _ := s.Resolve(ctx, auth.LocalIdentity)
	s.Resolve(ctx, "bob@example.com")
	if err := s.Rename(ctx, "local", "bob@example.com"); err == nil {
		t.Fatal("renamed onto an existing account")
	}
	if err := s.Rename(ctx, "nobody", "x@example.com"); err == nil {
		t.Fatal("renamed a missing account")
	}
	if err := s.Rename(ctx, "local", "Ana@Example.com"); err != nil {
		t.Fatal(err)
	}
	if u, _ := s.Resolve(ctx, "ana@example.com"); u.ID != local.ID {
		t.Fatalf("got %+v, want id %s", u, local.ID)
	}
}

func TestLoginLimitPerAddress(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "correct horse")
	// One address trying many accounts gets stopped, even below each account's limit.
	for i := range maxFailuresPerIP {
		s.Login(ctx, fmt.Sprintf("user%d@example.com", i), "guess", "203.0.113.7")
	}
	if _, err := s.Login(ctx, "ana@example.com", "correct horse", "203.0.113.7"); !errors.Is(err, ErrTooMany) {
		t.Fatalf("same address: %v", err)
	}
	if _, err := s.Login(ctx, "ana@example.com", "correct horse", "198.51.100.1"); err != nil {
		t.Fatalf("other address: %v", err)
	}
}

func TestFailureTableIsBounded(t *testing.T) {
	s := newService(t)
	s.failures = map[string]failures{}
	old := time.Now().Add(-2 * failWindow)
	for i := range maxTracked {
		s.failures[fmt.Sprintf("email:%d", i)] = failures{n: 1, since: old}
	}
	s.fail([]string{"email:new"})
	if len(s.failures) != 1 || s.failures["email:new"].n != 1 {
		t.Fatalf("expired entries kept: %d", len(s.failures))
	}
}

func TestClientIP(t *testing.T) {
	for _, c := range []struct{ remote, xff, want string }{
		{"203.0.113.7:5000", "", "203.0.113.7"},
		{"203.0.113.7:5000", "198.51.100.1", "203.0.113.7"},        // a client can't pick its address
		{"127.0.0.1:5000", "198.51.100.1", "198.51.100.1"},         // local proxy
		{"10.0.0.2:5000", "1.2.3.4, 198.51.100.1", "198.51.100.1"}, // the proxy's own hop, not the forged one
		{"127.0.0.1:5000", "", "127.0.0.1"},
		{"[::1]:5000", "2001:db8::1", "2001:db8::1"},
		{"127.0.0.1:5000", "not-an-ip", "127.0.0.1"},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientIP(r); got != c.want {
			t.Errorf("%s %q: got %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestSignOutAndDelete(t *testing.T) {
	s, ctx := newService(t), context.Background()
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	other, _ := s.SetPassword(ctx, "bob@example.com", "correct horse")
	a1, _ := s.NewSession(ctx, u.ID)
	a2, _ := s.NewSession(ctx, u.ID)
	b, _ := s.NewSession(ctx, other.ID)
	if n, err := s.SignOut(ctx, "Ana@example.com"); err != nil || n != 2 {
		t.Fatalf("signed out %d, %v", n, err)
	}
	for _, tok := range []string{a1, a2} {
		if _, err := s.Session(ctx, tok); err == nil {
			t.Fatal("session survived")
		}
	}
	if _, err := s.Session(ctx, b); err != nil {
		t.Fatal("another user's session ended")
	}

	s.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, 'x'), (?, 'y')`, "user/"+u.ID+"/providers", "user/"+other.ID+"/providers")
	s.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at, owner) VALUES ('p1', 'Paper', 0, 0, ?)`, u.ID)
	if err := s.Delete(ctx, "ana@example.com"); err == nil {
		t.Fatal("deleted an account that still owns projects")
	}
	s.DB.Exec(`DELETE FROM projects`)
	if err := s.Delete(ctx, "ana@example.com"); err != nil {
		t.Fatal(err)
	}
	var settings int
	s.DB.QueryRow(`SELECT count(*) FROM settings WHERE key LIKE 'user/%'`).Scan(&settings)
	if exists, _ := s.Exists(ctx, "ana@example.com"); exists || settings != 1 {
		t.Fatalf("exists=%v, settings left=%d", exists, settings)
	}
	if err := s.Delete(ctx, "ana@example.com"); err == nil {
		t.Fatal("deleted a missing account")
	}
	// A deleted account doesn't linger: the same email is a new, empty user.
	again, _ := s.Resolve(ctx, "ana@example.com")
	if again.ID == u.ID {
		t.Fatal("old account came back")
	}
}
