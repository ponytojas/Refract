package siwc

import (
	"cmp"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"html"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// NewHostID returns a fresh ext_agent_host_id (urn:uuid, UUIDv4). Generate once per host and persist.
func NewHostID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// LoginOptions configures the local loopback sign-in.
type LoginOptions struct {
	HostID string
	Saved  *Credentials // previous registration for this account: reuse its client id
	Port   int          // 0 = DefaultPort, negative = any free port
	// OpenBrowser opens the sign-in URL. A nil func or an error does not stop
	// the login: it keeps waiting, and OnURL lets the caller show the URL.
	OpenBrowser func(authURL string) error
	// OnURL is called once with the authorize URL, after the open attempt;
	// openErr is why the browser could not be opened, if it could not.
	OnURL func(authURL string, openErr error)
	// OnRegistration is called with a newly issued client id before the code
	// exchange, so the caller can save it: a failed exchange then retries with
	// the same registration (pass it back in Saved). An error stops the login.
	OnRegistration func(clientID string) error
	OAuth          *OAuth
	AuthorizeURL   string // "" = real endpoint
}

// Login runs the browser sign-in on a 127.0.0.1 listener and returns validated credentials.
func Login(ctx context.Context, o LoginOptions) (*Credentials, error) {
	if o.HostID == "" {
		return nil, errors.New("login: host id required")
	}
	port := max(cmp.Or(o.Port, DefaultPort), 0) // negative: any free port (tests)
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, fmt.Errorf("login: listen on 127.0.0.1 (is another login running?): %w", err)
	}
	defer ln.Close()
	// The docs require 127.0.0.1, never localhost; only the port may vary.
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d%s", ln.Addr().(*net.TCPAddr).Port, CallbackPath)

	verifier, state, nonce := randToken(), randToken(), randToken()
	sum := sha256.Sum256([]byte(verifier))
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {RegisterID},
		"redirect_uri":          {redirectURI},
		"scope":                 {Scopes},
		"resource":              {Resource},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
		"ext_agent_host_id":     {o.HostID},
	}
	if o.Saved != nil {
		q.Set("client_id", o.Saved.ClientID)
		// No id_token_hint: the URL is a process argument and lands in browser
		// history. The subject check after the exchange pins the account.
		if o.Saved.Email != "" {
			q.Set("login_hint", o.Saved.Email)
		}
	} else {
		q.Set("agent_name_hint", agentNameHint)
	}

	type result struct {
		code, clientID string
		err            error
	}
	done := make(chan result, 1)
	wantHost := fmt.Sprintf("127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+CallbackPath, func(w http.ResponseWriter, r *http.Request) {
		cspNonce := randToken()
		setCallbackHeaders(w, cspNonce)
		// A request that is not the real callback never consumes the pending login.
		reject := func(msg string) {
			http.Error(w, msg, http.StatusBadRequest)
		}
		p := r.URL.Query()
		if r.Host != wantHost {
			reject("This address is not the sign-in callback.")
			return
		}
		for _, k := range []string{"state", "code", "client_id", "error"} {
			if len(p[k]) > 1 {
				reject("The sign-in callback was malformed.")
				return
			}
		}
		if subtle.ConstantTimeCompare([]byte(p.Get("state")), []byte(state)) != 1 {
			reject("This sign-in link is not for the current sign-in.")
			return
		}
		if id := p.Get("client_id"); id != "" && (!clientIDPattern.MatchString(id) || id == RegisterID) {
			reject("The sign-in callback carried an invalid client.")
			return
		}
		var res result
		switch {
		case p.Get("error") == "access_denied":
			res.err = errors.New("sign-in was cancelled or denied")
		case p.Get("error") != "":
			res.err = fmt.Errorf("authorization failed: %s %s", p.Get("error"), p.Get("error_description"))
		case p.Get("code") == "":
			res.err = errors.New("callback without code")
		default:
			res = result{code: p.Get("code"), clientID: p.Get("client_id")}
		}
		if res.err != nil {
			w.WriteHeader(http.StatusBadRequest)
			writeCallbackPage(w, cspNonce, "Sign-in did not complete", res.err.Error()+". You can close this tab and try again.")
		} else {
			writeCallbackPage(w, cspNonce, "Signed in", "You can close this tab and go back to Refract.")
		}
		select {
		case done <- res:
		default:
		}
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go srv.Serve(ln)
	defer func() {
		// Let the answer to the real callback reach the browser before closing.
		c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(c)
	}()

	authURL := cmp.Or(o.AuthorizeURL, AuthorizeURL) + "?" + q.Encode()
	openErr := errors.New("no browser opener")
	if o.OpenBrowser != nil {
		openErr = o.OpenBrowser(authURL)
	}
	if o.OnURL != nil {
		o.OnURL(authURL, openErr)
	}

	var res result
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res = <-done:
	}
	if res.err != nil {
		return nil, res.err
	}

	clientID := res.clientID
	if o.Saved != nil {
		// Reauthorization may omit client_id; a different one means a mixed-up registration.
		if clientID != "" && clientID != o.Saved.ClientID {
			return nil, fmt.Errorf("callback returned client id %q, expected %q", clientID, o.Saved.ClientID)
		}
		clientID = o.Saved.ClientID
	} else if clientID == "" {
		return nil, errors.New("registration callback did not include the issued client_id")
	}

	if o.Saved == nil && o.OnRegistration != nil {
		// Persist the issued registration before the one-time code is spent.
		if err := o.OnRegistration(clientID); err != nil {
			return nil, fmt.Errorf("saving the registration: %w", err)
		}
	}
	tr, err := o.OAuth.Exchange(ctx, clientID, res.code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}
	idt, err := o.OAuth.VerifyIDToken(ctx, clientID, tr.IDToken, false)
	if err != nil {
		return nil, fmt.Errorf("id token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(nonce)) != 1 {
		return nil, errors.New("id token: nonce mismatch")
	}
	if o.Saved != nil && o.Saved.Subject != "" && idt.Subject != o.Saved.Subject {
		return nil, errors.New("signed in with a different ChatGPT account than the saved one; use -new to register it separately")
	}
	var claims struct {
		Email string `json:"email"`
	}
	idt.Claims(&claims)

	c := &Credentials{ClientID: clientID, Subject: idt.Subject, Email: claims.Email}
	c.apply(tr, time.Now())
	return c, nil
}

var clientIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,200}$`)

// setCallbackHeaders keeps the callback page out of caches and referrers, and
// allows only its own inline script and style (by nonce).
func setCallbackHeaders(w http.ResponseWriter, nonce string) {
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; script-src 'nonce-"+nonce+"'; style-src 'nonce-"+nonce+"'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
}

// writeCallbackPage shows the outcome and removes ?code=&state= from the address bar.
func writeCallbackPage(w http.ResponseWriter, nonce, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%[2]s</title>
<style nonce="%[1]s">body{font:16px system-ui,sans-serif;display:grid;place-items:center;min-height:100vh;margin:0;color:#222}main{max-width:26rem;padding:1.5rem;text-align:center}h1{font-size:1.25rem}</style>
<main><h1>%[2]s</h1><p>%[3]s</p></main>
<script nonce="%[1]s">history.replaceState(null,"",location.pathname)</script></html>`, nonce, html.EscapeString(title), html.EscapeString(msg))
}

func randToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
