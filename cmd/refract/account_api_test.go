package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mail"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/users"
)

// accountEnv is the sign-in and account routes served the way run serves them:
// the /api/auth/ handler outside the session guard, the table behind it.
type accountEnv struct {
	t       *testing.T
	srv     *httptest.Server
	login   *loginAPI
	people  *users.Service
	ps      *projects.Service
	fake    *mail.Fake
	outbox  *mail.Outbox
	flushes int
}

type envOpts struct {
	noMail bool // no outbox: the server can't send email
	signup bool
}

func TestMain(m *testing.M) {
	users.LowerHashCostForTests()
	os.Exit(m.Run())
}

func newAccountEnv(t *testing.T, o envOpts) *accountEnv {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	people := &users.Service{DB: db, Key: make([]byte, 32)}
	ps := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	e := &accountEnv{t: t, people: people, ps: ps, fake: &mail.Fake{}}
	e.login = &loginAPI{users: people, mode: mode.SelfHost, signup: o.signup, destroy: destroyer(ps)}
	if !o.noMail {
		e.login.public = "https://r.test"
		e.outbox = mail.NewOutbox(e.fake)
		e.login.mail = e.outbox
		t.Cleanup(func() { e.outbox.Drain(time.Second) })
	}
	app := http.NewServeMux()
	rt := &router{mux: app, acc: &access.Checker{DB: db}}
	(&projectsAPI{svc: ps}).routes(rt)
	e.login.accountRoutes(rt)
	mux := http.NewServeMux()
	mux.Handle("/api/auth/", e.login.handler())
	mux.Handle("/", people.SessionGuard(http.NotFoundHandler())(people.Middleware(auth.RequireCSRFHeader(app))))
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)
	return e
}

// browser is a client with its own cookies.
type browser struct {
	e *accountEnv
	c *http.Client
}

func (e *accountEnv) browser() *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{e, &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type answer struct {
	Status  int
	Body    string
	Cookies []*http.Cookie
}

func (a answer) kind() string {
	var v struct{ Error struct{ Kind string } }
	json.Unmarshal([]byte(a.Body), &v)
	return v.Error.Kind
}

func (b *browser) do(method, path string, body any, header ...string) answer {
	b.e.t.Helper()
	var r io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		r = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, b.e.srv.URL+path, r)
	req.Header.Set("X-Refract", "1")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := b.c.Do(req)
	if err != nil {
		b.e.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return answer{resp.StatusCode, string(raw), resp.Cookies()}
}

func (b *browser) post(path string, body any) answer { return b.do("POST", path, body) }

func (b *browser) expect(a answer, status int, kind string) {
	b.e.t.Helper()
	if a.Status != status || a.kind() != kind {
		b.e.t.Fatalf("got %d %s, want %d %q", a.Status, a.Body, status, kind)
	}
}

// mails waits for the outbox to hand over everything queued so far.
func (e *accountEnv) mails() []mail.Message {
	e.t.Helper()
	e.flushes++
	e.outbox.Send(mail.Message{Subject: "flush"})
	for i := 0; i < 200; i++ {
		var sent []mail.Message
		flushed := 0
		for _, m := range e.fake.Sent() {
			if m.Subject == "flush" {
				flushed++
			} else {
				sent = append(sent, m)
			}
		}
		if flushed == e.flushes {
			return sent
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("the outbox didn't deliver")
	return nil
}

var linkToken = regexp.MustCompile(`https://r\.test/(verify|reset)#token=([A-Za-z0-9_-]{43})\b`)

func tokenIn(t *testing.T, m mail.Message, kind string) string {
	t.Helper()
	got := linkToken.FindStringSubmatch(m.Body)
	if got == nil || got[1] != kind {
		t.Fatalf("no %s link in %q", kind, m.Body)
	}
	return got[2]
}

func TestAuthConfig(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		opts                  envOpts
		noPublic              bool
		wantSignup, wantReset bool
	}{
		{"open with mail", envOpts{signup: true}, false, true, true},
		{"closed with mail", envOpts{}, false, false, true},
		{"open, no mail", envOpts{signup: true, noMail: true}, false, true, false}, // run never builds this: signup needs mail
		{"no mail", envOpts{noMail: true}, false, false, false},
		{"mail, no public address", envOpts{}, true, false, false},
	} {
		e := newAccountEnv(t, tc.opts)
		if tc.noPublic {
			e.login.public = ""
		}
		a := e.browser().do("GET", "/api/auth/config", nil)
		var got map[string]any
		json.Unmarshal([]byte(a.Body), &got)
		if got["mode"] != "selfhost" || got["signup"] != tc.wantSignup || got["reset"] != tc.wantReset {
			t.Errorf("%s: config = %s", tc.name, a.Body)
		}
	}
}

func TestSignUpAndConfirm(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	b := e.browser()
	a := b.post("/api/auth/signup", map[string]string{"email": "ana@example.com", "password": "correct horse", "lang": "es"})
	if a.Status != 204 || len(a.Cookies) != 0 {
		t.Fatalf("sign-up: %d %s cookies=%v", a.Status, a.Body, a.Cookies)
	}
	sent := e.mails()
	if len(sent) != 1 || sent[0].To != "ana@example.com" || !strings.Contains(sent[0].Subject, "Confirma") {
		t.Fatalf("messages = %+v", sent)
	}
	tok := tokenIn(t, sent[0], "verify")

	b.expect(b.post("/api/auth/verify", map[string]string{"token": tok}), 401, "password_required")
	a = b.post("/api/auth/verify", map[string]string{"token": tok, "password": "correct horse"})
	if a.Status != 204 || len(a.Cookies) == 0 {
		t.Fatalf("verify: %d %s", a.Status, a.Body)
	}
	if acct, _ := e.people.Lookup(context.Background(), "ana@example.com"); !acct.Verified {
		t.Error("not verified")
	}
	b.expect(b.post("/api/auth/verify", map[string]string{"token": tok, "password": "correct horse"}), 400, "invalid_token")
}

func TestUnverifiedCanSignInButNotCreate(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	b := e.browser()
	b.post("/api/auth/signup", map[string]string{"email": "ana@example.com", "password": "correct horse", "lang": "en"})
	sentAfterSignUp := e.mails() // the account is made after the answer
	if a := b.post("/api/auth/login", map[string]string{"email": "ana@example.com", "password": "correct horse"}); a.Status != 204 {
		t.Fatalf("login: %d %s", a.Status, a.Body)
	}
	b.expect(b.post("/api/projects", map[string]string{"name": "Paper"}), 403, "email_unverified")
	b.expect(b.post("/api/projects/import", nil), 403, "email_unverified")
	if a := b.do("GET", "/api/projects", nil); a.Status != 200 {
		t.Errorf("listing: %d", a.Status)
	}
	tok := tokenIn(t, sentAfterSignUp[0], "verify")
	if a := b.post("/api/auth/verify", map[string]string{"token": tok}); a.Status != 204 || len(a.Cookies) != 0 {
		t.Fatalf("verify while signed in: %d %s cookies=%v", a.Status, a.Body, a.Cookies)
	}
	if a := b.post("/api/projects", map[string]string{"name": "Paper"}); a.Status != 200 && a.Status != 201 {
		t.Errorf("create after verifying: %d %s", a.Status, a.Body)
	}
}

func TestNoEnumeration(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	e.people.SetPassword(context.Background(), "ana@example.com", "correct horse")
	e.fake.Block = make(chan struct{}) // the mail server hangs: answers must not wait for it
	defer func() { close(e.fake.Block) }()
	b := e.browser()

	for _, path := range []string{"/api/auth/signup", "/api/auth/forgot"} {
		call := func(email string) answer {
			start := time.Now()
			a := b.post(path, map[string]string{"email": email, "password": "another pass", "lang": "en"})
			if time.Since(start) > 2*time.Second {
				t.Errorf("%s waited for the mail server", path)
			}
			return a
		}
		known, unknown := call("ana@example.com"), call("nobody@example.com")
		if known.Status != unknown.Status || known.Body != unknown.Body || len(known.Cookies) != 0 || len(unknown.Cookies) != 0 {
			t.Errorf("%s tells them apart: %+v vs %+v", path, known, unknown)
		}
	}
}

func TestNoEnumerationMessages(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	e.people.SetPassword(context.Background(), "ana@example.com", "correct horse")
	b := e.browser()
	b.post("/api/auth/forgot", map[string]string{"email": "nobody@example.com"})
	if n := len(e.mails()); n != 0 {
		t.Fatalf("a reset went to an unknown address (%d messages)", n)
	}
	b.post("/api/auth/forgot", map[string]string{"email": "ana@example.com"})
	sent := e.mails()
	if len(sent) != 1 || sent[0].To != "ana@example.com" {
		t.Fatalf("messages = %+v", sent)
	}
	tokenIn(t, sent[0], "reset")
	if strings.Contains(sent[0].Body, "Someone tried to create") {
		t.Error("a plain reset carries the sign-up line")
	}

	b.post("/api/auth/signup", map[string]string{"email": "ana@example.com", "password": "attacker pass", "lang": "en"})
	sent = e.mails()
	if len(sent) != 2 || !strings.Contains(sent[1].Body, "already has one") {
		t.Fatalf("a sign-up for an existing address sent %+v", sent)
	}
	tokenIn(t, sent[1], "reset")
	if _, err := e.people.Login(context.Background(), "ana@example.com", "correct horse", ""); err != nil {
		t.Errorf("the sign-up changed the password: %v", err)
	}
}

func TestResetFlow(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	e.people.SetPassword(context.Background(), "ana@example.com", "old password")
	old := e.browser()
	if a := old.post("/api/auth/login", map[string]string{"email": "ana@example.com", "password": "old password"}); a.Status != 204 {
		t.Fatal(a)
	}
	b := e.browser()
	b.post("/api/auth/forgot", map[string]string{"email": "ana@example.com"})
	tok := tokenIn(t, e.mails()[0], "reset")

	b.expect(b.post("/api/auth/reset", map[string]string{"token": tok, "password": "short"}), 400, "other")
	a := b.post("/api/auth/reset", map[string]string{"token": tok, "password": "new password"})
	if a.Status != 204 || len(a.Cookies) == 0 {
		t.Fatalf("reset: %d %s", a.Status, a.Body)
	}
	if a := old.do("GET", "/api/account", nil); a.Status != 401 {
		t.Errorf("the old session after a reset: %d", a.Status)
	}
	if a := b.do("GET", "/api/account", nil); a.Status != 200 {
		t.Errorf("the session the reset made: %d", a.Status)
	}
	if a := e.browser().post("/api/auth/login", map[string]string{"email": "ana@example.com", "password": "new password"}); a.Status != 204 {
		t.Errorf("new password: %d", a.Status)
	}
	b.expect(b.post("/api/auth/reset", map[string]string{"token": tok, "password": "third password"}), 400, "invalid_token")
}

func TestMailThrottle(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	e.people.SetPassword(context.Background(), "ana@example.com", "correct horse")
	b := e.browser()
	// Past 3 for an address the answer is the same, and nothing is sent.
	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		for i := 0; i < 5; i++ {
			if a := b.post("/api/auth/forgot", map[string]string{"email": email}); a.Status != 204 || a.Body != "" {
				t.Fatalf("%s #%d: %d %s", email, i+1, a.Status, a.Body)
			}
		}
	}
	if n := len(e.mails()); n != 3 {
		t.Errorf("%d emails sent, want 3 (to ana)", n)
	}
	// Past 20 from one network address the request is refused.
	e.people.DB.Exec(`DELETE FROM login_failures`)
	for i := 0; i < 20; i++ {
		if a := b.post("/api/auth/forgot", map[string]string{"email": "u" + strings.Repeat("x", i) + "@example.com"}); a.Status != 204 {
			t.Fatalf("#%d: %d", i+1, a.Status)
		}
	}
	b.expect(b.post("/api/auth/forgot", map[string]string{"email": "late@example.com"}), 429, "too_many_emails")
}

// The request path doesn't depend on whether the address has an account: with
// the outbox's worker busy, an answer comes back and nothing has been written
// for either address yet.
func TestAnswersBeforeAnyAccountWork(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	e.people.SetPassword(context.Background(), "ana@example.com", "correct horse")
	release := make(chan struct{})
	e.outbox.Run(func(context.Context) { <-release })
	b := e.browser()
	count := func(q string) (n int) {
		e.people.DB.QueryRow(q).Scan(&n)
		return n
	}
	users0, tokens0 := count(`SELECT count(*) FROM users`), count(`SELECT count(*) FROM account_tokens`)
	for _, email := range []string{"ana@example.com", "nobody@example.com"} {
		b.post("/api/auth/forgot", map[string]string{"email": email})
		b.post("/api/auth/signup", map[string]string{"email": email, "password": "another pass", "lang": "en"})
	}
	if u, tk := count(`SELECT count(*) FROM users`), count(`SELECT count(*) FROM account_tokens`); u != users0 || tk != tokens0 {
		t.Errorf("the handlers wrote before answering: users %d -> %d, tokens %d -> %d", users0, u, tokens0, tk)
	}
	close(release)
	if n := len(e.mails()); n != 3 { // reset + sign-up reset to ana; verify to nobody
		t.Errorf("%d messages after the work ran, want 3", n)
	}
	if count(`SELECT count(*) FROM users`) != users0+1 {
		t.Error("the sign-up for a new address made no account")
	}
}

// Someone else asking for links doesn't cancel the one the owner holds.
func TestFloodDoesNotCancelTheOwnersLink(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	e.people.SetPassword(context.Background(), "ana@example.com", "correct horse")
	b := e.browser()
	b.post("/api/auth/forgot", map[string]string{"email": "ana@example.com"})
	tok := tokenIn(t, e.mails()[0], "reset")
	b.post("/api/auth/signup", map[string]string{"email": "ana@example.com", "password": "attacker pass", "lang": "en"})
	e.mails()
	if a := b.post("/api/auth/reset", map[string]string{"token": tok, "password": "owners new pass"}); a.Status != 204 {
		t.Fatalf("the owner's earlier link: %d %s", a.Status, a.Body)
	}
}

// Deleting the account finishes even when the client goes away part way.
func TestDeleteAccountSurvivesDisconnect(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	ctx := context.Background()
	u, _ := e.people.SetPassword(ctx, "ana@example.com", "correct horse")
	as := auth.WithUser(ctx, u)
	e.ps.Create(as, "One")
	e.ps.Create(as, "Two")
	reqCtx, cancel := context.WithCancel(as)
	defer cancel()
	inner := e.login.destroy
	e.login.destroy = func(c context.Context, id string) error {
		cancel() // the client hangs up as the first project goes
		return inner(c, id)
	}
	body, _ := json.Marshal(map[string]string{"password": "correct horse"})
	r := httptest.NewRequest("POST", "/api/account/delete", bytes.NewReader(body)).WithContext(reqCtx)
	w := httptest.NewRecorder()
	e.login.deleteAccount(w, r)
	if w.Code != 204 {
		t.Fatalf("delete: %d %s", w.Code, w.Body.String())
	}
	if _, err := e.people.Lookup(ctx, "ana@example.com"); err == nil {
		t.Error("the account survived the disconnect")
	}
	if n, _ := e.people.OwnedCount(ctx, u.ID); n != 0 {
		t.Errorf("%d projects left", n)
	}
}

func TestMailUnavailable(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	e.people.SetPassword(context.Background(), "ana@example.com", "correct horse")
	b := e.browser()
	b.expect(b.post("/api/auth/forgot", map[string]string{"email": "ana@example.com"}), 503, "mail_unavailable")
	b.expect(b.post("/api/auth/signup", map[string]string{"email": "bob@example.com", "password": "correct horse", "lang": "en"}), 403, "signup_closed")
	b.post("/api/auth/login", map[string]string{"email": "ana@example.com", "password": "correct horse"})
	b.expect(b.post("/api/account/verify-email", nil), 503, "mail_unavailable")
}

func TestSignUpClosed(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	b := e.browser()
	b.expect(b.post("/api/auth/signup", map[string]string{"email": "bob@example.com", "password": "correct horse", "lang": "en"}), 403, "signup_closed")
	if exists, _ := e.people.Exists(context.Background(), "bob@example.com"); exists {
		t.Error("an account was created")
	}
	if n := len(e.mails()); n != 0 {
		t.Errorf("%d messages sent", n)
	}
	// Bad input is 400 whatever the policy, and whether or not the address exists.
	b.expect(b.post("/api/auth/signup", map[string]string{"email": "bob", "password": "correct horse", "lang": "en"}), 400, "other")
}

func TestResendAndLang(t *testing.T) {
	e := newAccountEnv(t, envOpts{signup: true})
	b := e.browser()
	b.post("/api/auth/signup", map[string]string{"email": "ana@example.com", "password": "correct horse", "lang": "es"})
	e.mails()
	b.post("/api/auth/login", map[string]string{"email": "ana@example.com", "password": "correct horse"})
	e.mails()

	if a := b.post("/api/account/verify-email", nil); a.Status != 204 {
		t.Fatal(a)
	}
	sent := e.mails()
	if len(sent) != 2 || !strings.Contains(sent[1].Subject, "Confirma") {
		t.Fatalf("messages = %+v", sent)
	}
	b.expect(b.do("PUT", "/api/account/lang", map[string]string{"lang": "fr"}), 400, "other")
	if a := b.do("PUT", "/api/account/lang", map[string]string{"lang": "en"}); a.Status != 204 {
		t.Fatal(a)
	}
	b.post("/api/account/verify-email", nil)
	sent = e.mails()
	if len(sent) != 3 || !strings.Contains(sent[2].Subject, "Confirm your email") {
		t.Fatalf("after switching to English: %+v", sent)
	}
	// Once verified, nothing is sent.
	e.people.DB.Exec(`DELETE FROM login_failures`) // the throttle has counted three emails
	e.people.MarkVerified(context.Background(), mustAccount(t, e, "ana@example.com").ID)
	if a := b.post("/api/account/verify-email", nil); a.Status != 204 {
		t.Fatal(a)
	}
	if n := len(e.mails()); n != 3 {
		t.Errorf("a verified account was sent a link (%d messages)", n)
	}
	var acct struct {
		Email, Lang string
		Verified    bool `json:"email_verified"`
		Mail        bool
	}
	json.Unmarshal([]byte(b.do("GET", "/api/account", nil).Body), &acct)
	if acct.Email != "ana@example.com" || acct.Lang != "en" || !acct.Verified || !acct.Mail {
		t.Errorf("account = %+v", acct)
	}
}

func mustAccount(t *testing.T, e *accountEnv, email string) users.Account {
	t.Helper()
	a, err := e.people.Lookup(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestDeleteAccount(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	ctx := context.Background()
	u, _ := e.people.SetPassword(ctx, "ana@example.com", "correct horse")
	e.people.SetLang(ctx, u.ID, "es")
	as := auth.WithUser(ctx, u)
	live, err := e.ps.Create(as, "Live")
	if err != nil {
		t.Fatal(err)
	}
	trashed, _ := e.ps.Create(as, "Trashed")
	if err := e.ps.Trash(as, trashed.ID); err != nil {
		t.Fatal(err)
	}
	other, _ := e.people.SetPassword(ctx, "bob@example.com", "correct horse")
	bobs, _ := e.ps.Create(auth.WithUser(ctx, other), "Bob's")

	b := e.browser()
	b.post("/api/auth/login", map[string]string{"email": "ana@example.com", "password": "correct horse"})
	e.people.NewSession(ctx, u.ID)

	b.expect(b.post("/api/account/delete", map[string]string{"password": "wrong password"}), 403, "wrong_password")
	if _, err := e.people.Lookup(ctx, "ana@example.com"); err != nil {
		t.Fatal("deleted with a wrong password")
	}
	a := b.post("/api/account/delete", map[string]string{"password": "correct horse"})
	if a.Status != 204 {
		t.Fatalf("delete: %d %s", a.Status, a.Body)
	}
	cleared := false
	for _, c := range a.Cookies {
		cleared = cleared || (c.Name == users.SessionCookie && c.MaxAge < 0)
	}
	if !cleared {
		t.Error("the session cookie wasn't cleared")
	}
	if _, err := e.people.Lookup(ctx, "ana@example.com"); err == nil {
		t.Error("the account is still there")
	}
	for _, id := range []string{live.ID, trashed.ID} {
		if _, err := os.Stat(e.ps.Path(id)); err == nil {
			t.Errorf("project %s's files are still there", id)
		}
		var n int
		e.people.DB.QueryRow(`SELECT count(*) FROM projects WHERE id = ?`, id).Scan(&n)
		if n != 0 {
			t.Errorf("project %s's row is still there", id)
		}
	}
	if _, err := os.Stat(e.ps.Path(bobs.ID)); err != nil {
		t.Error("another account's project went")
	}
	var sessions, tokens int
	e.people.DB.QueryRow(`SELECT count(*) FROM sessions WHERE user_id = ?`, u.ID).Scan(&sessions)
	e.people.DB.QueryRow(`SELECT count(*) FROM account_tokens`).Scan(&tokens)
	if sessions != 0 || tokens != 0 {
		t.Errorf("%d sessions, %d tokens left", sessions, tokens)
	}
	sent := e.mails()
	if len(sent) != 1 || sent[0].To != "ana@example.com" || !strings.Contains(sent[0].Subject, "Se ha eliminado") {
		t.Errorf("messages = %+v", sent)
	}
	if a := b.do("GET", "/api/account", nil); a.Status != 401 {
		t.Errorf("the old cookie: %d", a.Status)
	}
}

func TestDeleteAccountWithoutPassword(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	ctx := context.Background()
	u, _ := e.people.Resolve(ctx, "ana@example.com") // as an identity provider would
	token, _ := e.people.NewSession(ctx, u.ID)
	b := e.browser()
	url, _ := http.NewRequest("GET", e.srv.URL, nil)
	b.c.Jar.SetCookies(url.URL, []*http.Cookie{{Name: users.SessionCookie, Value: token, Path: "/"}})

	e.people.DB.Exec(`UPDATE sessions SET authenticated_at = ?`, time.Now().Add(-11*time.Minute).Unix())
	b.expect(b.post("/api/account/delete", nil), 403, "reauth_required")
	var acct struct {
		Recent bool `json:"recent_sign_in"`
	}
	json.Unmarshal([]byte(b.do("GET", "/api/account", nil).Body), &acct)
	if acct.Recent {
		t.Error("an old sign-in counts as recent")
	}
	e.people.DB.Exec(`UPDATE sessions SET authenticated_at = ?`, time.Now().Unix())
	if a := b.post("/api/account/delete", nil); a.Status != 204 {
		t.Fatalf("delete after a fresh sign-in: %d %s", a.Status, a.Body)
	}
}

func TestOIDCThen(t *testing.T) {
	e := newAccountEnv(t, envOpts{})
	// The start route needs a provider; what matters here is the cookie that
	// carries the destination, which only "account" may set.
	for _, tc := range []struct{ cookie, want string }{{"account", "/settings/account"}, {"/evil", "/"}, {"", "/"}} {
		r := httptest.NewRequest("GET", "/api/auth/oidc/callback", nil)
		if tc.cookie != "" {
			r.AddCookie(&http.Cookie{Name: oidcThenCookie, Value: tc.cookie})
		}
		if got := callbackTarget(r); got != tc.want {
			t.Errorf("callbackTarget(%q) = %q, want %q", tc.cookie, got, tc.want)
		}
	}
	_ = e
	for q, want := range map[string]bool{"then=account": true, "then=/evil": false, "then=": false, "": false} {
		if got := thenAccount(httptest.NewRequest("GET", "/api/auth/oidc/start?"+q, nil)); got != want {
			t.Errorf("thenAccount(%q) = %v", q, got)
		}
	}
	_ = e
}

// A plain sign-in expires a destination cookie left by an unfinished
// "then=account" one.
func TestOIDCStartClearsThenCookie(t *testing.T) {
	var idp *httptest.Server
	idp = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": idp.URL, "authorization_endpoint": idp.URL + "/auth",
			"token_endpoint": idp.URL + "/token", "jwks_uri": idp.URL + "/jwks"})
	}))
	defer idp.Close()
	a := &loginAPI{oidc: &auth.OIDC{Issuer: idp.URL, ClientID: "c", ClientSecret: "s", RedirectURL: "https://r.test/api/auth/oidc/callback"}}
	then := func(query string) *http.Cookie {
		w := httptest.NewRecorder()
		a.oidcStart(w, httptest.NewRequest("GET", "/api/auth/oidc/start"+query, nil))
		for _, c := range w.Result().Cookies() {
			if c.Name == oidcThenCookie {
				return c
			}
		}
		return nil
	}
	if c := then("?then=account"); c == nil || c.Value != "account" || c.MaxAge <= 0 {
		t.Errorf("then=account cookie = %+v", c)
	}
	for _, q := range []string{"", "?then=/evil"} {
		if c := then(q); c == nil || c.MaxAge >= 0 || c.Value != "" {
			t.Errorf("%q: cookie = %+v, want it expired", q, c)
		}
	}
}
