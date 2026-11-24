package siwc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/ponytojas/Refract/internal/store"
)

// browser is a fake OpenBrowser that records the authorize URL and calls the
// loopback listener back (with callback's overrides, if any).
func (f *fakeAuth) browser(urls *[]string, callback func(q url.Values) url.Values) func(string) error {
	return func(authURL string) error {
		*urls = append(*urls, authURL)
		u, _ := url.Parse(authURL)
		q := u.Query()
		f.nonce = q.Get("nonce")
		cb := url.Values{"state": {q.Get("state")}, "code": {"the-code"}, "client_id": {testClient}}
		if callback != nil {
			cb = callback(cb)
		}
		go http.Get(q.Get("redirect_uri") + "?" + cb.Encode())
		return nil
	}
}

func (f *fakeAuth) loginOpts(open func(string) error) LoginOptions {
	return LoginOptions{HostID: "h", Port: -1, OAuth: f.oauth(), AuthorizeURL: "https://auth.invalid/a", OpenBrowser: open}
}

func timed(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestAuthorizeURLCarriesNoToken(t *testing.T) {
	f := newFakeAuth(t)
	saved := &Credentials{ClientID: testClient, Subject: "user-1", Email: "u@example.com",
		AccessToken: "saved-access", RefreshToken: "saved-refresh", IDToken: f.idToken(testClient, "user-1", "", time.Now().Add(time.Hour))}
	var urls []string
	o := f.loginOpts(f.browser(&urls, nil))
	o.Saved = saved
	if _, err := Login(timed(t), o); err != nil {
		t.Fatal(err)
	}
	u := urls[0]
	for _, secret := range []string{saved.IDToken, saved.AccessToken, saved.RefreshToken} {
		if strings.Contains(u, secret) {
			t.Fatalf("authorize URL carries a token: %s", u)
		}
	}
	q, _ := url.Parse(u)
	if q.Query().Has("id_token_hint") || q.Query().Get("login_hint") != "u@example.com" {
		t.Fatalf("want login_hint and no id_token_hint: %s", u)
	}
}

func TestRegistrationSurvivesFailedExchange(t *testing.T) {
	f := newFakeAuth(t)
	f.exchangeFails = 1
	var urls []string
	var saved []string
	o := f.loginOpts(f.browser(&urls, nil))
	o.OnRegistration = func(id string) error { saved = append(saved, id); return nil }
	if _, err := Login(timed(t), o); err == nil {
		t.Fatal("first exchange should fail")
	}
	if len(saved) != 1 || saved[0] != testClient {
		t.Fatalf("registration not persisted before the exchange: %v", saved)
	}

	// Retry with only the client id saved: no new registration.
	o2 := f.loginOpts(f.browser(&urls, nil))
	o2.Saved = &Credentials{ClientID: saved[0]}
	o2.OnRegistration = func(string) error { t.Error("registered again"); return nil }
	c, err := Login(timed(t), o2)
	if err != nil {
		t.Fatal(err)
	}
	if c.ClientID != testClient || len(f.exchanges) != 2 || f.exchanges[0] != f.exchanges[1] {
		t.Fatalf("exchanges used %v", f.exchanges)
	}
	q, _ := url.Parse(urls[1])
	if q.Query().Get("client_id") != testClient || q.Query().Has("agent_name_hint") {
		t.Fatalf("retry authorized with a new registration: %s", urls[1])
	}
}

func TestRegistrationSaveFailureStopsBeforeExchange(t *testing.T) {
	f := newFakeAuth(t)
	var urls []string
	o := f.loginOpts(f.browser(&urls, nil))
	o.OnRegistration = func(string) error { return errors.New("disk full") }
	if _, err := Login(timed(t), o); err == nil || len(f.exchanges) != 0 {
		t.Fatalf("err=%v exchanges=%v", err, f.exchanges)
	}
}

func TestTokenResponseValidation(t *testing.T) {
	cases := map[string]func(map[string]any){
		"no scope":            func(r map[string]any) { delete(r, "scope") },
		"not bearer":          func(r map[string]any) { r["token_type"] = "mac" },
		"no token_type":       func(r map[string]any) { delete(r, "token_type") },
		"zero expires_in":     func(r map[string]any) { r["expires_in"] = 0 },
		"no refresh token":    func(r map[string]any) { delete(r, "refresh_token") },
		"no id token":         func(r map[string]any) { delete(r, "id_token") },
		"negative expires_in": func(r map[string]any) { r["expires_in"] = -5 },
	}
	for name, mut := range cases {
		t.Run("exchange: "+name, func(t *testing.T) {
			f := newFakeAuth(t)
			f.mutate = func(grant string, r map[string]any) {
				if grant == "authorization_code" {
					mut(r)
				}
			}
			var urls []string
			_, err := Login(timed(t), f.loginOpts(f.browser(&urls, nil)))
			if err == nil || !strings.Contains(err.Error(), "sign in again") {
				t.Fatalf("err = %v", err)
			}
		})
		if name == "no id token" {
			continue // a refresh may omit it
		}
		t.Run("refresh: "+name, func(t *testing.T) {
			f := newFakeAuth(t)
			s := newSession(t, f)
			s.Import(context.Background(), f.credFile("user-1", time.Now().Add(-time.Minute)))
			f.mutate = func(grant string, r map[string]any) { mut(r) }
			if _, err := s.AccessToken(context.Background(), false); err == nil {
				t.Fatal("accepted an invalid response")
			}
			if c, _ := s.Credentials(context.Background()); c.RefreshToken != "refresh-0" || c.Pending != nil {
				t.Fatalf("invalid response changed the stored record: %+v", c)
			}
		})
	}
	t.Run("bearer is case-insensitive", func(t *testing.T) {
		f := newFakeAuth(t)
		f.mutate = func(_ string, r map[string]any) { r["token_type"] = "bearer" }
		var urls []string
		if _, err := Login(timed(t), f.loginOpts(f.browser(&urls, nil))); err != nil {
			t.Fatal(err)
		}
	})
}

// failingKeys is a key set whose fetch can be switched off, like an unreachable JWKS.
type failingKeys struct {
	mu   sync.Mutex
	down bool
	real oidc.KeySet
}

func (k *failingKeys) VerifySignature(ctx context.Context, jws string) ([]byte, error) {
	k.mu.Lock()
	down := k.down
	k.mu.Unlock()
	if down {
		return nil, errors.New("fetching keys Get jwks: connection refused")
	}
	return k.real.VerifySignature(ctx, jws)
}

func (k *failingKeys) set(down bool) { k.mu.Lock(); k.down = down; k.mu.Unlock() }

func expiredSession(t *testing.T, f *fakeAuth) *Session {
	s := newSession(t, f)
	if _, err := s.Import(context.Background(), f.credFile("user-1", time.Now().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRefreshVerifiesIDToken(t *testing.T) {
	ctx := context.Background()
	t.Run("same account is accepted and stored", func(t *testing.T) {
		f := newFakeAuth(t)
		s := expiredSession(t, f)
		f.refreshID = func(cid string) string { return f.idToken(cid, "user-1", "", time.Now().Add(time.Hour)) }
		if tok, err := s.AccessToken(ctx, false); err != nil || tok != "access-1" {
			t.Fatalf("%q, %v", tok, err)
		}
		c, _ := s.Credentials(ctx)
		if c.Pending != nil || c.RefreshToken != "refresh-1" || c.IDToken == "" {
			t.Fatalf("not promoted: %+v", c)
		}
	})
	t.Run("another subject is rejected and credentials kept", func(t *testing.T) {
		f := newFakeAuth(t)
		s := expiredSession(t, f)
		f.refreshID = func(cid string) string { return f.idToken(cid, "intruder", "", time.Now().Add(time.Hour)) }
		if _, err := s.AccessToken(ctx, false); !errors.Is(err, ErrAccountMismatch) {
			t.Fatalf("err = %v", err)
		}
		c, _ := s.Credentials(ctx)
		if c.Subject != "user-1" || c.ClientID != testClient || c.Pending != nil || c.AccessToken != "access-0" {
			t.Fatalf("stored credentials changed: %+v", c)
		}
	})
	t.Run("bad signature is rejected", func(t *testing.T) {
		f, other := newFakeAuth(t), newFakeAuth(t)
		s := expiredSession(t, f)
		f.refreshID = func(cid string) string { return other.idToken(cid, "user-1", "", time.Now().Add(time.Hour)) }
		_, err := s.AccessToken(ctx, false)
		if err == nil || errors.Is(err, ErrVerifyUnavailable) {
			t.Fatalf("err = %v", err)
		}
		if c, _ := s.Credentials(ctx); c.Pending != nil || c.AccessToken != "access-0" {
			t.Fatalf("forged tokens were kept: %+v", c)
		}
	})
	t.Run("wrong audience is rejected", func(t *testing.T) {
		f := newFakeAuth(t)
		s := expiredSession(t, f)
		f.refreshID = func(string) string { return f.idToken("oaiapp_other", "user-1", "", time.Now().Add(time.Hour)) }
		if _, err := s.AccessToken(ctx, false); err == nil {
			t.Fatal("accepted another audience")
		}
	})
}

func TestInterruptedRefreshResumes(t *testing.T) {
	ctx := context.Background()
	f := newFakeAuth(t)
	s := expiredSession(t, f)
	keys := &failingKeys{real: s.OAuth.Keys}
	s.OAuth.Keys = keys
	// Short-lived id token: the resumed check must judge it at receive time.
	f.refreshID = func(cid string) string { return f.idToken(cid, "user-1", "", time.Now().Add(time.Second)) }

	keys.set(true)
	_, err := s.AccessToken(ctx, false)
	if !errors.Is(err, ErrVerifyUnavailable) || errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("err = %v, want a retryable verification error", err)
	}
	c, _ := s.Credentials(ctx)
	if c.Pending == nil || c.Pending.RefreshToken != "refresh-1" || c.RefreshToken != "refresh-0" {
		t.Fatalf("rotation not checkpointed: %+v", c)
	}

	// A "restart": a new Session over the same database, after the id token expired.
	time.Sleep(1200 * time.Millisecond)
	keys.set(false)
	s2 := &Session{DB: s.DB, Key: s.Key, OAuth: s.OAuth, AllowedSubject: s.AllowedSubject}
	tok, err := s2.AccessToken(ctx, false)
	if err != nil || tok != "access-1" {
		t.Fatalf("resume: %q, %v", tok, err)
	}
	if f.refresh.Load() != 1 {
		t.Fatalf("the spent refresh token was used again (%d refreshes)", f.refresh.Load())
	}
	if c, _ := s2.Credentials(ctx); c.Pending != nil || c.RefreshToken != "refresh-1" {
		t.Fatalf("not promoted: %+v", c)
	}
}

func TestPreviousRecordShapeStillLoads(t *testing.T) {
	ctx := context.Background()
	f := newFakeAuth(t)
	s := newSession(t, f)
	// What an earlier version stored: no "pending" key.
	old := `{"client_id":"` + testClient + `","access_token":"a","refresh_token":"refresh-0","id_token":"i","scope":"` + Scopes +
		`","expires_at":"2099-01-01T00:00:00Z","sub":"user-1","email":"u@example.com"}`
	sealed, err := store.Seal(s.Key, []byte(old), credKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(ctx, s.DB, credKey, sealed); err != nil {
		t.Fatal(err)
	}
	c, err := s.Credentials(ctx)
	if err != nil || c.Pending != nil || c.RefreshToken != "refresh-0" || c.Subject != "user-1" {
		t.Fatalf("%+v, %v", c, err)
	}
	if tok, err := s.AccessToken(ctx, false); err != nil || tok != "a" {
		t.Fatalf("%q, %v", tok, err)
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), "pending") {
		t.Fatal("empty pending written to the record")
	}
}

func TestCallbackIgnoresStrayRequests(t *testing.T) {
	f := newFakeAuth(t)
	var good *http.Response
	var goodBody string
	var strays []int
	finished := make(chan struct{})
	open := func(authURL string) error {
		u, _ := url.Parse(authURL)
		q := u.Query()
		f.nonce = q.Get("nonce")
		redirect, _ := url.Parse(q.Get("redirect_uri"))
		st := url.QueryEscape(q.Get("state"))
		go func() {
			defer close(finished)
			get := func(rawQuery, host string) int {
				req, _ := http.NewRequest("GET", redirect.Scheme+"://"+redirect.Host+redirect.Path+"?"+rawQuery, nil)
				if host != "" {
					req.Host = host
				}
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Error(err)
					return 0
				}
				defer resp.Body.Close()
				return resp.StatusCode
			}
			base := "state=" + st + "&code=the-code&client_id=" + testClient
			strays = append(strays,
				get(base, "localhost:"+redirect.Port()),                     // wrong Host
				get(base, "evil.example"),                                   // wrong Host
				get(base+"&state=other", ""),                                // repeated state
				get(base+"&code=second", ""),                                // repeated code
				get(base+"&client_id=oaiapp_two", ""),                       // repeated client_id
				get("state="+st+"&code=the-code&client_id=bad%20id%21", ""), // invalid client_id
				get("state="+st+"&code=the-code&client_id="+RegisterID, ""), // refused entrypoint id
				get("state=wrong&code=the-code&client_id="+testClient, ""),  // wrong state
			)
			resp, err := http.Get(redirect.Scheme + "://" + redirect.Host + redirect.Path + "?" + base)
			if err != nil {
				t.Error(err)
				return
			}
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			good, goodBody = resp, string(b)
		}()
		return nil
	}
	c, err := Login(timed(t), f.loginOpts(open))
	if err != nil {
		t.Fatalf("a stray request consumed the pending login: %v", err)
	}
	if c.ClientID != testClient {
		t.Fatalf("%+v", c)
	}
	select {
	case <-finished:
	case <-time.After(3 * time.Second):
		t.Fatal("callback goroutine did not finish")
	}
	for i, code := range strays {
		if code != http.StatusBadRequest {
			t.Errorf("stray request %d answered %d", i, code)
		}
	}
	if good == nil {
		t.Fatal("no answer to the real callback")
	}
	h := good.Header
	csp := h.Get("Content-Security-Policy")
	if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" ||
		!strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "http") {
		t.Fatalf("headers: %v", h)
	}
	if !strings.Contains(goodBody, "history.replaceState") || !strings.Contains(goodBody, "You can close this tab") {
		t.Fatalf("page: %s", goodBody)
	}
}

func TestOpenerErrorStillExposesURL(t *testing.T) {
	f := newFakeAuth(t)
	var got string
	var gotErr error
	o := f.loginOpts(func(string) error { return errors.New("no display") })
	o.OnURL = func(u string, openErr error) {
		got, gotErr = u, openErr
		// The user opens the URL by hand.
		var urls []string
		f.browser(&urls, nil)(u)
	}
	if _, err := Login(timed(t), o); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "https://auth.invalid/a?") || gotErr == nil || gotErr.Error() != "no display" {
		t.Fatalf("url=%q err=%v", got, gotErr)
	}
}

func TestRevokeRetriesOnce(t *testing.T) {
	old := revokeRetryDelay
	revokeRetryDelay = time.Millisecond
	t.Cleanup(func() { revokeRetryDelay = old })
	ctx := context.Background()

	f := newFakeAuth(t)
	s := newSession(t, f)
	s.Import(ctx, f.credFile("user-1", time.Now().Add(time.Hour)))
	f.revokeFails = 1
	if err := s.SignOut(ctx); err != nil || f.revoked.Load() != 2 {
		t.Fatalf("one 503 should be retried: err=%v calls=%d", err, f.revoked.Load())
	}

	f2 := newFakeAuth(t)
	s2 := newSession(t, f2)
	s2.Import(ctx, f2.credFile("user-1", time.Now().Add(time.Hour)))
	f2.revokeFails = 5
	err := s2.SignOut(ctx)
	if err == nil || f2.revoked.Load() != 2 {
		t.Fatalf("want a failure after exactly one retry: err=%v calls=%d", err, f2.revoked.Load())
	}
	if c, _ := s2.Credentials(ctx); c.RefreshToken != "" {
		t.Fatal("tokens kept locally after a failed revocation")
	}
	// A network error is retried too.
	f3 := newFakeAuth(t)
	o := f3.oauth()
	calls := 0
	o.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return nil, errors.New("connection reset")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	if err := o.Revoke(ctx, &Credentials{ClientID: testClient, RefreshToken: "r"}); err != nil || calls != 2 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestNoTokensInLogs(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx := context.Background()
	f := newFakeAuth(t)
	s := expiredSession(t, f)
	idt := f.idToken(testClient, "intruder", "", time.Now().Add(time.Hour))
	f.refreshID = func(string) string { return idt }
	s.AccessToken(ctx, false) // mismatch
	f.refreshID = nil
	keys := &failingKeys{real: s.OAuth.Keys, down: true}
	s.OAuth.Keys = keys
	f.refreshID = func(cid string) string { return f.idToken(cid, "user-1", "", time.Now().Add(time.Hour)) }
	s.ExpireNow(ctx)
	s.AccessToken(ctx, false) // outage
	keys.set(false)
	s.AccessToken(ctx, false)
	s.SignOut(ctx)
	for _, secret := range []string{"access-", "refresh-", idt, "eyJ"} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("a token reached the logs: %q", buf.String())
		}
	}
}
