package siwc

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// finish plays the browser for a URL from Logins.Start: it answers the
// callback with the fake server's code, or with overrides.
func finish(t *testing.T, f *fakeAuth, authURL string, override url.Values) {
	t.Helper()
	u, _ := url.Parse(authURL)
	q := u.Query()
	f.mu.Lock()
	f.nonce = q.Get("nonce")
	f.mu.Unlock()
	cb := url.Values{"state": {q.Get("state")}, "code": {"the-code"}, "client_id": {testClient}}
	for k, v := range override {
		cb[k] = v
	}
	resp, err := http.Get(q.Get("redirect_uri") + "?" + cb.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func waitState(t *testing.T, l *Logins, owner, want string) LoginState {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := l.Status(owner)
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("state = %+v, want %s", st, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestLoginsStoresTheCredential(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	f := newFakeAuth(t)
	s := newSession(t, f)
	s.Owner = "ana"
	l := &Logins{Port: -1, AuthorizeURL: "https://auth.invalid/authorize"}
	if st := l.Status("ana"); st.State != LoginIdle {
		t.Fatalf("before = %+v", st)
	}
	started, err := l.Start(context.Background(), s, false)
	u := started.URL
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u, "https://auth.invalid/authorize?") || strings.Contains(u, "refresh") {
		t.Fatalf("url = %s", u)
	}
	if st := l.Status("ana"); st.State != LoginPending {
		t.Fatalf("while waiting = %+v", st)
	}
	finish(t, f, u, nil)
	waitState(t, l, "ana", LoginDone)
	c, err := s.Credentials(context.Background())
	if err != nil || c == nil || c.RefreshToken != "refresh-0" || c.Subject != "user-1" || c.Email != "u@example.com" {
		t.Fatalf("stored = %+v, %v", c, err)
	}
	for _, tok := range []string{"access-0", "refresh-0", "the-code"} {
		if strings.Contains(logs.String(), tok) {
			t.Errorf("log contains %q:\n%s", tok, logs.String())
		}
	}

	// Signing in again reuses the stored registration.
	started, err = l.Start(context.Background(), s, false)
	u = started.URL
	if err != nil {
		t.Fatal(err)
	}
	if q, _ := url.Parse(u); q.Query().Get("client_id") != testClient || q.Query().Get("login_hint") != "u@example.com" {
		t.Fatalf("second url = %s", u)
	}
	l.Cancel("ana")
}

func TestLoginsOpensTheBrowser(t *testing.T) {
	f := newFakeAuth(t)
	s := newSession(t, f)
	var opened string
	l := &Logins{Port: -1, AuthorizeURL: "https://auth.invalid/a", OpenBrowser: func(u string) error { opened = u; return nil }}
	started, err := l.Start(context.Background(), s, false)
	if err != nil {
		t.Fatal(err)
	}
	if !started.Opened || opened != started.URL {
		t.Fatalf("started = %+v, opened %q", started, opened)
	}
	l.Cancel("")
	l.OpenBrowser = func(string) error { return errors.New("no browser") }
	if started, err = l.Start(context.Background(), s, false); err != nil || started.Opened {
		t.Fatalf("a failed open must leave it to the page: %+v %v", started, err)
	}
	l.Cancel("")
}

func TestLoginsOneAtATime(t *testing.T) {
	f := newFakeAuth(t)
	ana, bea := newSession(t, f), newSession(t, f)
	ana.Owner, bea.Owner = "ana", "bea"
	bea.DB, bea.Key = ana.DB, ana.Key
	l := &Logins{Port: -1, AuthorizeURL: "https://auth.invalid/a"}
	first, err := l.Start(context.Background(), ana, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Start(context.Background(), bea, false); !errors.Is(err, ErrLoginBusy) {
		t.Fatalf("second account: err = %v, want busy", err)
	}
	// The same account starting again replaces its stale attempt.
	second, err := l.Start(context.Background(), ana, false)
	if err != nil || second.URL == first.URL {
		t.Fatalf("restart: %v", err)
	}
	if st := l.Status("ana"); st.State != LoginPending {
		t.Fatalf("after restart = %+v", st)
	}
	// Cancel frees the server for the other account.
	l.Cancel("bea") // not bea's: no effect
	if st := l.Status("ana"); st.State != LoginPending {
		t.Fatalf("bea cancelled ana's sign-in: %+v", st)
	}
	l.Cancel("ana")
	if st := l.Status("ana"); st.State != LoginFailed || st.Kind != FailCancelled {
		t.Fatalf("after cancel = %+v", st)
	}
	if _, err := l.Start(context.Background(), bea, false); err != nil {
		t.Fatalf("after cancel: %v", err)
	}
	l.Cancel("bea")
}

func TestLoginsFailures(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		f := newFakeAuth(t)
		s := newSession(t, f)
		l := &Logins{Port: -1, Timeout: 50 * time.Millisecond, AuthorizeURL: "https://auth.invalid/a"}
		if _, err := l.Start(context.Background(), s, false); err != nil {
			t.Fatal(err)
		}
		if st := waitState(t, l, "", LoginFailed); st.Kind != FailTimeout {
			t.Fatalf("state = %+v", st)
		}
	})
	t.Run("denied", func(t *testing.T) {
		f := newFakeAuth(t)
		s := newSession(t, f)
		l := &Logins{Port: -1, AuthorizeURL: "https://auth.invalid/a"}
		started, err := l.Start(context.Background(), s, false)
		u := started.URL
		if err != nil {
			t.Fatal(err)
		}
		finish(t, f, u, url.Values{"error": {"access_denied"}, "code": nil})
		if st := waitState(t, l, "", LoginFailed); st.Kind != FailCancelled {
			t.Fatalf("state = %+v", st)
		}
	})
	t.Run("port in use", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer ln.Close()
		f := newFakeAuth(t)
		s := newSession(t, f)
		l := &Logins{Port: ln.Addr().(*net.TCPAddr).Port}
		if _, err := l.Start(context.Background(), s, false); err == nil {
			t.Fatal("started on a taken port")
		}
		if st := l.Status(""); st.State != LoginFailed || st.Kind != FailPortBusy {
			t.Fatalf("state = %+v", st)
		}
	})
	t.Run("another account than the saved one", func(t *testing.T) {
		f := newFakeAuth(t)
		s := newSession(t, f)
		s.AllowedSubject = ""
		if err := s.Store(context.Background(), &Credentials{ClientID: testClient, Subject: "someone-else"}); err != nil {
			t.Fatal(err)
		}
		l := &Logins{Port: -1, AuthorizeURL: "https://auth.invalid/a"}
		started, err := l.Start(context.Background(), s, false)
		u := started.URL
		if err != nil {
			t.Fatal(err)
		}
		finish(t, f, u, nil)
		if st := waitState(t, l, "", LoginFailed); st.Kind != FailOtherAccount {
			t.Fatalf("state = %+v", st)
		}
		// fresh registers the account that signed in.
		if started, err = l.Start(context.Background(), s, true); err != nil {
			t.Fatal(err)
		}
		finish(t, f, started.URL, nil)
		waitState(t, l, "", LoginDone)
		if c, _ := s.Credentials(context.Background()); c.Subject != "user-1" {
			t.Fatalf("stored subject = %q", c.Subject)
		}
	})
	t.Run("account not allowed", func(t *testing.T) {
		f := newFakeAuth(t)
		s := newSession(t, f)
		s.AllowedSubject = "someone-else"
		l := &Logins{Port: -1, AuthorizeURL: "https://auth.invalid/a"}
		started, err := l.Start(context.Background(), s, false)
		u := started.URL
		if err != nil {
			t.Fatal(err)
		}
		finish(t, f, u, nil)
		st := waitState(t, l, "", LoginFailed)
		if st.Kind != FailOther || !strings.Contains(st.Message, "REFRACT_ALLOWED_SUBJECT") {
			t.Fatalf("state = %+v", st)
		}
		if c, _ := s.Credentials(context.Background()); c != nil && c.RefreshToken != "" {
			t.Fatal("stored a refused account's tokens")
		}
	})
}
