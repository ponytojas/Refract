package users

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

func TestMain(m *testing.M) {
	pbkdf2Iter = 1000 // the real count makes every login in these tests take ~0.3 s
	os.Exit(m.Run())
}

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
	if a, _ := s.AccountByID(ctx, u.ID); !a.HasPassword {
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
	s, ctx := newService(t), context.Background()
	fill := func(since int64) {
		s.DB.Exec(`DELETE FROM login_failures`)
		_, err := s.DB.Exec(`INSERT INTO login_failures (key, count, since)
			SELECT 'email:' || i, 1, ? FROM (WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?) SELECT i FROM n)`, since, maxTracked)
		if err != nil {
			t.Fatal(err)
		}
	}
	count := func(key string) (n int) {
		s.DB.QueryRow(`SELECT count FROM login_failures WHERE key = ?`, key).Scan(&n)
		return
	}
	// Full of expired rows: they're dropped, and the new key counts.
	fill(time.Now().Add(-2 * failWindow).Unix())
	s.count(ctx, []string{"email:new"})
	var rows int
	s.DB.QueryRow(`SELECT count(*) FROM login_failures`).Scan(&rows)
	if rows != 1 || count("email:new") != 1 {
		t.Fatalf("rows %d, new %d", rows, count("email:new"))
	}
	// Full of recent rows: existing keys still count, new ones aren't added.
	fill(time.Now().Unix())
	s.count(ctx, []string{"email:1", "email:another"})
	if count("email:1") != 2 || count("email:another") != 0 {
		t.Fatalf("existing %d, new %d", count("email:1"), count("email:another"))
	}
}

// The counts outlive the server process, and an operator can clear them.
func TestLockoutSurvivesRestartAndUnlocks(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.SetPassword(ctx, "ana@example.com", "correct horse")
	for range maxFailures {
		s.Login(ctx, "ana@example.com", "wrong", "")
	}
	restarted := &Service{DB: s.DB}
	if _, err := restarted.Login(ctx, "ana@example.com", "correct horse", ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("after restart: %v", err)
	}
	if ok, err := restarted.Unlock(ctx, "ANA@example.com"); !ok || err != nil {
		t.Fatalf("unlock: %v %v", ok, err)
	}
	if _, err := restarted.Login(ctx, "ana@example.com", "correct horse", ""); err != nil {
		t.Fatalf("after unlock: %v", err)
	}
	if ok, _ := restarted.Unlock(ctx, "ana@example.com"); ok {
		t.Fatal("unlocked an account with no failures")
	}
	for range maxFailuresPerIP {
		s.Login(ctx, "x@example.com", "wrong", "203.0.113.9")
	}
	if n, err := s.UnlockAll(ctx); err != nil || n != 2 { // the account and the address
		t.Fatalf("unlock all: %d %v", n, err)
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
	for _, id := range []string{u.ID, other.ID} {
		if _, err := s.DB.Exec(`INSERT INTO ai_usage (at, user_id, kind, model, input_tokens, cached_tokens, output_tokens, reasoning_tokens) VALUES (1, ?, 'agent', 'm', 1, 0, 1, 0)`, id); err != nil {
			t.Fatal(err)
		}
	}
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
	var usageRows int
	var usageUser string
	s.DB.QueryRow(`SELECT count(*), coalesce(max(user_id), '') FROM ai_usage`).Scan(&usageRows, &usageUser)
	if usageRows != 1 || usageUser != other.ID {
		t.Fatalf("usage left: %d rows, user %q; want only %s's", usageRows, usageUser, other.ID)
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

func TestRemove(t *testing.T) {
	s, ctx := newService(t), context.Background()
	s.Now = func() time.Time { return time.Unix(1_800_000_000, 0) }
	u, _ := s.SetPassword(ctx, "ana@example.com", "correct horse")
	other, _ := s.SetPassword(ctx, "bob@example.com", "correct horse")
	s.NewSession(ctx, u.ID)
	s.DB.Exec(`INSERT INTO settings (key, value) VALUES (?, 'x')`, "user/"+u.ID+"/providers")
	s.DB.Exec(`INSERT INTO ai_usage (at, user_id, kind, model, input_tokens, cached_tokens, output_tokens, reasoning_tokens) VALUES (1, ?, 'agent', 'm', 1, 0, 1, 0)`, u.ID)
	s.RequestReset(ctx, "ana@example.com")
	s.AllowMail(ctx, "ana@example.com", "")
	s.DB.Exec(`INSERT INTO projects (id, name, created_at, updated_at, owner) VALUES ('p1', 'Alpha', 0, 0, ?), ('p2', 'Beta', 0, 0, ?), ('p3', 'Other', 0, 0, ?)`, u.ID, u.ID, other.ID)
	s.DB.Exec(`UPDATE projects SET deleted_at = 5 WHERE id = 'p2'`)

	owned, err := s.OwnedProjects(ctx, u.ID)
	if err != nil || len(owned) != 2 || owned[0].Trashed || !owned[1].Trashed {
		t.Fatalf("owned = %+v, %v", owned, err)
	}

	var destroyed []string
	del := func(ctx context.Context, id string) error {
		destroyed = append(destroyed, id)
		if id == "p2" && len(destroyed) == 2 && failSecond {
			return errors.New("busy")
		}
		_, err := s.DB.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
		return err
	}
	failSecond = true
	if n, err := s.Remove(ctx, u.ID, del); err == nil || !strings.Contains(err.Error(), "Beta") || n != 1 {
		t.Fatalf("a failing destroy: n=%d err=%v; want an error naming the project", n, err)
	}
	if _, err := s.Lookup(ctx, "ana@example.com"); err != nil {
		t.Fatal("the account went although a project couldn't be deleted")
	}
	failSecond, destroyed = false, nil
	if n, err := s.Remove(ctx, u.ID, del); err != nil || n != 1 {
		t.Fatalf("remove = %d, %v", n, err)
	}
	if _, err := s.Lookup(ctx, "ana@example.com"); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("account still there: %v", err)
	}
	for table, where := range map[string]string{
		"sessions": "user_id", "ai_usage": "user_id", "account_tokens": "user_id",
	} {
		var n int
		s.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+where+` = ?`, u.ID).Scan(&n)
		if n != 0 {
			t.Errorf("%s: %d rows left", table, n)
		}
	}
	var settings, throttle, projectsLeft int
	s.DB.QueryRow(`SELECT count(*) FROM settings WHERE key LIKE 'user/%'`).Scan(&settings)
	s.DB.QueryRow(`SELECT count(*) FROM login_failures WHERE key LIKE '%ana@%'`).Scan(&throttle)
	s.DB.QueryRow(`SELECT count(*) FROM projects`).Scan(&projectsLeft)
	if settings != 0 || throttle != 0 || projectsLeft != 1 {
		t.Errorf("left: %d settings, %d throttle rows, %d projects (bob's one stays)", settings, throttle, projectsLeft)
	}

	// An account with no projects goes at once; a project that appears
	// between listing and deleting stops it.
	if n, err := s.Remove(ctx, other.ID, func(context.Context, string) error { return nil }); !errors.Is(err, ErrOwnsProjects) || n != 1 {
		t.Errorf("a destroy that left the project: n=%d, %v", n, err)
	}
	s.DB.Exec(`DELETE FROM projects`)
	if _, err := s.Remove(ctx, other.ID, del); err != nil {
		t.Fatal(err)
	}
}

var failSecond bool
