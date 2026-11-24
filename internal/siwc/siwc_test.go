package siwc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	jose "github.com/go-jose/go-jose/v4"

	"github.com/ponytojas/Refract/internal/store"
)

const testClient = "oaiapp_test"

// fakeAuth is a stand-in for auth.openai.com's token and revoke endpoints.
type fakeAuth struct {
	t        *testing.T
	key      *rsa.PrivateKey
	srv      *httptest.Server
	refresh  atomic.Int32 // refresh grants served
	revoked  atomic.Int32
	failWith string // token endpoint error code, if set
	failCode int
	nonce    string // nonce to embed in id tokens from code exchange
	sub      string
	scope    string

	mu            sync.Mutex
	mutate        func(grant string, resp map[string]any) // tweak a token response
	exchanges     []string                                // client ids seen on code exchanges
	exchangeFails int                                     // fail this many code exchanges with a 503
	refreshID     func(clientID string) string            // id token to return on refresh, if any
	revokeFails   int                                     // fail this many revocations with a 503
}

func newFakeAuth(t *testing.T) *fakeAuth {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeAuth{t: t, key: key, sub: "user-1", scope: Scopes}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuth) oauth() *OAuth {
	return &OAuth{
		TokenURL:  f.srv.URL + "/token",
		RevokeURL: f.srv.URL + "/revoke",
		Keys:      &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{f.key.Public()}},
		HTTP:      f.srv.Client(),
	}
}

func (f *fakeAuth) idToken(aud, sub, nonce string, exp time.Time) string {
	signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, nil)
	claims := map[string]any{"iss": Issuer, "aud": aud, "sub": sub, "exp": exp.Unix(), "email": "u@example.com"}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	payload, _ := json.Marshal(claims)
	jws, _ := signer.Sign(payload)
	s, _ := jws.CompactSerialize()
	return s
}

func (f *fakeAuth) serve(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.URL.Path == "/revoke" {
		f.revoked.Add(1)
		if f.revokeFails > 0 {
			f.revokeFails--
			w.WriteHeader(503)
		}
		return
	}
	if r.Form.Get("resource") != Resource {
		f.t.Errorf("token request without resource: %v", r.Form)
	}
	if f.failWith != "" {
		w.WriteHeader(f.failCode)
		fmt.Fprintf(w, `{"error":%q}`, f.failWith)
		return
	}
	resp := map[string]any{"expires_in": 3600, "scope": f.scope, "token_type": "Bearer"}
	grant := r.Form.Get("grant_type")
	switch grant {
	case "authorization_code":
		f.exchanges = append(f.exchanges, r.Form.Get("client_id"))
		if f.exchangeFails > 0 {
			f.exchangeFails--
			w.WriteHeader(503)
			fmt.Fprint(w, `{"error":"temporarily_unavailable"}`)
			return
		}
		if r.Form.Get("code") != "the-code" || r.Form.Get("code_verifier") == "" {
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":"invalid_grant"}`)
			return
		}
		resp["access_token"], resp["refresh_token"] = "access-0", "refresh-0"
		resp["id_token"] = f.idToken(r.Form.Get("client_id"), f.sub, f.nonce, time.Now().Add(time.Hour))
	case "refresh_token":
		n := f.refresh.Add(1)
		time.Sleep(20 * time.Millisecond) // widen the race window
		resp["access_token"] = fmt.Sprintf("access-%d", n)
		resp["refresh_token"] = fmt.Sprintf("refresh-%d", n)
		if f.refreshID != nil {
			resp["id_token"] = f.refreshID(r.Form.Get("client_id"))
		}
	}
	if f.mutate != nil {
		f.mutate(grant, resp)
	}
	json.NewEncoder(w).Encode(resp)
}

func newSession(t *testing.T, f *fakeAuth) *Session {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key := make([]byte, 32)
	rand.Read(key)
	return &Session{DB: db, Key: key, OAuth: f.oauth(), AllowedSubject: "user-1"}
}

func (f *fakeAuth) credFile(sub string, expiresAt time.Time) []byte {
	b, _ := json.Marshal(Credentials{
		ClientID: testClient, AccessToken: "access-0", RefreshToken: "refresh-0",
		// Expired ID token on purpose: import must still accept it.
		IDToken: f.idToken(testClient, sub, "", time.Now().Add(-time.Hour)),
		Scope:   Scopes, ExpiresAt: expiresAt, Subject: "spoofed",
	})
	return b
}

func TestImport(t *testing.T) {
	ctx := context.Background()
	f := newFakeAuth(t)

	t.Run("allowed account is stored encrypted", func(t *testing.T) {
		s := newSession(t, f)
		c, err := s.Import(ctx, f.credFile("user-1", time.Now().Add(time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		if c.Subject != "user-1" || c.Email != "u@example.com" {
			t.Fatalf("identity not taken from the id token: %+v", c)
		}
		raw, _ := store.GetSetting(ctx, s.DB, credKey)
		if strings.Contains(raw, "refresh-0") {
			t.Fatal("refresh token stored in plaintext")
		}
	})
	t.Run("other account rejected", func(t *testing.T) {
		if _, err := newSession(t, f).Import(ctx, f.credFile("someone-else", time.Now())); err == nil {
			t.Fatal("imported a non-allowlisted account")
		}
	})
	t.Run("no allowlist: each user imports their own account", func(t *testing.T) {
		s := newSession(t, f)
		s.AllowedSubject = ""
		if _, err := s.Import(ctx, f.credFile("someone-else", time.Now())); err != nil {
			t.Fatalf("rejected without an allowlist: %v", err)
		}
	})
	t.Run("credentials are per user", func(t *testing.T) {
		a := newSession(t, f)
		a.Owner = "ua"
		if _, err := a.Import(ctx, f.credFile("user-1", time.Now())); err != nil {
			t.Fatal(err)
		}
		b := &Session{DB: a.DB, Key: a.Key, OAuth: a.OAuth, AllowedSubject: a.AllowedSubject, Owner: "ub"}
		if c, err := b.Credentials(ctx); err != nil || c != nil {
			t.Fatalf("user b sees user a's credentials: %+v, %v", c, err)
		}
		if c, err := a.Credentials(ctx); err != nil || c == nil || c.Subject != "user-1" {
			t.Fatalf("user a lost them: %+v, %v", c, err)
		}
	})
	t.Run("forged id token rejected", func(t *testing.T) {
		other := newFakeAuth(t)
		if _, err := newSession(t, f).Import(ctx, other.credFile("user-1", time.Now())); err == nil {
			t.Fatal("imported a token signed by another key")
		}
	})
}

func TestRefreshIsSingleFlightAndPersisted(t *testing.T) {
	ctx := context.Background()
	f := newFakeAuth(t)
	s := newSession(t, f)
	if _, err := s.Import(ctx, f.credFile("user-1", time.Now().Add(-time.Minute))); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	tokens := make([]string, 10)
	for i := range tokens {
		wg.Go(func() {
			tok, err := s.AccessToken(ctx, false)
			if err != nil {
				t.Error(err)
			}
			tokens[i] = tok
		})
	}
	wg.Wait()

	if n := f.refresh.Load(); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
	for _, tok := range tokens {
		if tok != "access-1" {
			t.Fatalf("got token %q, want access-1", tok)
		}
	}
	c, _ := s.Credentials(ctx)
	if c.RefreshToken != "refresh-1" {
		t.Fatalf("rotated refresh token not persisted: %q", c.RefreshToken)
	}

	// Forced refresh (the "token expired" test path) rotates again.
	if err := s.ExpireNow(ctx); err != nil {
		t.Fatal(err)
	}
	if tok, err := s.AccessToken(ctx, false); err != nil || tok != "access-2" {
		t.Fatalf("after expiry: %q, %v", tok, err)
	}
}

func TestRefreshFailures(t *testing.T) {
	ctx := context.Background()

	t.Run("terminal error clears tokens", func(t *testing.T) {
		f := newFakeAuth(t)
		s := newSession(t, f)
		s.Import(ctx, f.credFile("user-1", time.Now().Add(-time.Minute)))
		f.failWith, f.failCode = "refresh_token_reused", 400
		if _, err := s.AccessToken(ctx, false); !errors.Is(err, ErrNotSignedIn) {
			t.Fatalf("err = %v, want ErrNotSignedIn", err)
		}
		c, _ := s.Credentials(ctx)
		if c.RefreshToken != "" || c.ClientID != testClient {
			t.Fatalf("want tokens cleared and registration kept, got %+v", c)
		}
	})
	t.Run("transient error keeps tokens", func(t *testing.T) {
		f := newFakeAuth(t)
		s := newSession(t, f)
		s.Import(ctx, f.credFile("user-1", time.Now().Add(-time.Minute)))
		f.failWith, f.failCode = "temporarily_unavailable", 503
		if _, err := s.AccessToken(ctx, false); err == nil || errors.Is(err, ErrNotSignedIn) {
			t.Fatalf("err = %v, want a transient error", err)
		}
		if c, _ := s.Credentials(ctx); c.RefreshToken != "refresh-0" {
			t.Fatal("transient failure dropped the refresh token")
		}
	})
}

func TestSignOut(t *testing.T) {
	ctx := context.Background()
	f := newFakeAuth(t)
	s := newSession(t, f)
	s.Import(ctx, f.credFile("user-1", time.Now().Add(time.Hour)))
	if err := s.SignOut(ctx); err != nil {
		t.Fatal(err)
	}
	if f.revoked.Load() != 1 {
		t.Fatal("refresh token not revoked")
	}
	if _, err := s.AccessToken(ctx, false); !errors.Is(err, ErrNotSignedIn) {
		t.Fatalf("after sign-out err = %v", err)
	}
}

// login drives Login with a fake browser that calls back with the given overrides.
func login(t *testing.T, f *fakeAuth, saved *Credentials, callback func(q url.Values) url.Values) (*Credentials, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return Login(ctx, LoginOptions{
		HostID: "urn:uuid:test", Saved: saved, Port: -1, OAuth: f.oauth(), AuthorizeURL: "https://auth.invalid/authorize",
		OpenBrowser: func(authURL string) error {
			u, _ := url.Parse(authURL)
			q := u.Query()
			f.nonce = q.Get("nonce")
			cb := url.Values{"state": {q.Get("state")}, "code": {"the-code"}, "client_id": {testClient}}
			if callback != nil {
				cb = callback(cb)
			}
			go http.Get(q.Get("redirect_uri") + "?" + cb.Encode())
			return nil
		},
	})
}

func TestLogin(t *testing.T) {
	t.Run("registration", func(t *testing.T) {
		c, err := login(t, newFakeAuth(t), nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if c.ClientID != testClient || c.Subject != "user-1" || c.RefreshToken != "refresh-0" || !c.PlanGranted() {
			t.Fatalf("unexpected credentials: %+v", c)
		}
	})
	t.Run("plan usage declined", func(t *testing.T) {
		f := newFakeAuth(t)
		f.scope = "openid profile email offline_access"
		c, err := login(t, f, nil, nil)
		if err != nil || c.PlanGranted() {
			t.Fatalf("want sign-in kept without plan usage, got %+v %v", c, err)
		}
	})
	t.Run("access denied", func(t *testing.T) {
		_, err := login(t, newFakeAuth(t), nil, func(q url.Values) url.Values {
			return url.Values{"state": q["state"], "error": {"access_denied"}}
		})
		if err == nil {
			t.Fatal("want error")
		}
	})
	t.Run("different client id on reauthorization", func(t *testing.T) {
		_, err := login(t, newFakeAuth(t), &Credentials{ClientID: "oaiapp_other"}, nil)
		if err == nil {
			t.Fatal("accepted a mismatched client id")
		}
	})
	t.Run("bad nonce", func(t *testing.T) {
		f := newFakeAuth(t)
		_, err := Login(context.Background(), LoginOptions{
			HostID: "h", Port: -1, OAuth: f.oauth(), AuthorizeURL: "https://auth.invalid/a",
			OpenBrowser: func(authURL string) error {
				u, _ := url.Parse(authURL)
				q := u.Query()
				f.nonce = "not-the-nonce"
				go http.Get(q.Get("redirect_uri") + "?" + url.Values{"state": {q.Get("state")}, "code": {"the-code"}, "client_id": {testClient}}.Encode())
				return nil
			},
		})
		if err == nil {
			t.Fatal("accepted an id token with the wrong nonce")
		}
	})
}
