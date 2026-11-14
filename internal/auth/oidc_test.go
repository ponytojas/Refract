package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// fakeIdP is a minimal OpenID provider. claims are added to each ID token.
func fakeIdP(t *testing.T, claims map[string]any) (*httptest.Server, *string) {
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	var srv *httptest.Server
	var challenge, nonce string
	lastAuth := new(string)
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": srv.URL, "authorization_endpoint": srv.URL + "/auth", "token_endpoint": srv.URL + "/token",
			"jwks_uri": srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: key.Public(), Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("/auth", func(w http.ResponseWriter, r *http.Request) {
		*lastAuth = r.URL.String()
		challenge, nonce = r.URL.Query().Get("code_challenge"), r.URL.Query().Get("nonce")
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if r.Form.Get("code") != "the-code" || base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		c := map[string]any{"iss": srv.URL, "aud": "refract", "sub": "u1", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "nonce": nonce}
		for k, v := range claims {
			c[k] = v
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "id_token": signJWT(t, key, c)})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, lastAuth
}

func signIn(t *testing.T, claims map[string]any, tamper func(cookie, state string) (string, string)) (string, bool, error) {
	srv, lastAuth := fakeIdP(t, claims)
	o := &OIDC{Issuer: srv.URL, ClientID: "refract", ClientSecret: "s", RedirectURL: "https://refract.test/api/auth/oidc/callback"}
	ctx := context.Background()
	to, cookie, err := o.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	http.Get(to) // the browser visits the provider
	u, _ := url.Parse(*lastAuth)
	state := u.Query().Get("state")
	if u.Query().Get("code_challenge_method") != "S256" || u.Query().Get("redirect_uri") != o.RedirectURL {
		t.Fatalf("auth request %s", *lastAuth)
	}
	if tamper != nil {
		cookie, state = tamper(cookie, state)
	}
	return o.Finish(ctx, cookie, state, "the-code")
}

func TestOIDC(t *testing.T) {
	email, verified, err := signIn(t, map[string]any{"email": "Ana@Example.com", "email_verified": true}, nil)
	if err != nil || email != "ana@example.com" || !verified {
		t.Fatalf("got %q %v %v", email, verified, err)
	}
	// A provider that doesn't say is let through, but not counted as vouching.
	if email, verified, err := signIn(t, map[string]any{"email": "ana@example.com"}, nil); err != nil || email != "ana@example.com" || verified {
		t.Fatalf("absent claim: %q %v %v", email, verified, err)
	}
	if _, _, err := signIn(t, map[string]any{"email": "ana@example.com", "email_verified": false}, nil); err == nil {
		t.Fatal("unverified email accepted")
	}
	if _, _, err := signIn(t, map[string]any{}, nil); err == nil {
		t.Fatal("token without email accepted")
	}
	wrongState := func(c, s string) (string, string) { return c, s + "x" }
	if _, _, err := signIn(t, map[string]any{"email": "ana@example.com"}, wrongState); err == nil {
		t.Fatal("state mismatch accepted")
	}
	wrongNonce := func(c, s string) (string, string) {
		p := strings.SplitN(c, ".", 3)
		return p[0] + ".other." + p[2], s
	}
	if _, _, err := signIn(t, map[string]any{"email": "ana@example.com"}, wrongNonce); err == nil {
		t.Fatal("nonce mismatch accepted")
	}
	wrongVerifier := func(c, s string) (string, string) {
		p := strings.SplitN(c, ".", 3)
		return p[0] + "." + p[1] + ".other-verifier-other-verifier-other-verifier", s
	}
	if _, _, err := signIn(t, map[string]any{"email": "ana@example.com"}, wrongVerifier); err == nil {
		t.Fatal("PKCE verifier mismatch accepted")
	}
}

func TestAllowed(t *testing.T) {
	list := []string{"ana@example.com", " @Lab.org "}
	for email, want := range map[string]bool{"ana@example.com": true, "bob@lab.org": true, "bob@example.com": false, "eve@evillab.org": false} {
		if Allowed(list, email) != want {
			t.Errorf("%s: want %v", email, want)
		}
	}
}
