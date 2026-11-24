// Package siwc implements Sign in with ChatGPT (open-source plan usage flow):
// local loopback login, credential import on the server, rotating refresh and
// sign-out. Endpoints and rules follow
// https://developers.openai.com/siwc/token-sharing-open-source.
package siwc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	Issuer        = "https://auth.openai.com"
	AuthorizeURL  = Issuer + "/api/accounts/authorize"
	TokenURL      = Issuer + "/api/accounts/oauth/token"
	RevokeURL     = Issuer + "/api/accounts/oauth/revoke"
	JWKSURL       = Issuer + "/.well-known/jwks.json"
	Resource      = "https://api.openai.com/v1"
	Scopes        = "openid profile email offline_access resource.invoke chatgpt.tokens.use.direct"
	PlanScope     = "chatgpt.tokens.use.direct"
	RegisterID    = "dynamic_agent_client" // first-time registration entrypoint, never saved
	ManageUsage   = "https://chatgpt.com/settings/usage"
	CallbackPath  = "/auth/callback"
	DefaultPort   = 1455
	agentNameHint = "Refract"
)

// Credentials is one registration's token set. The same JSON is the file
// written by `refract login` and the (encrypted) record stored by the server.
type Credentials struct {
	ClientID          string    `json:"client_id"`
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	IDToken           string    `json:"id_token"`
	Scope             string    `json:"scope"`
	ExpiresAt         time.Time `json:"expires_at"`
	EarliestRefreshAt time.Time `json:"earliest_refresh_at,omitzero"`
	Subject           string    `json:"sub"`
	Email             string    `json:"email,omitempty"`
	// Pending is a rotated token set received but not yet verified. The old
	// refresh token is spent once it exists, so it is saved first and promoted
	// after the ID token checks out (or resumed after a restart).
	Pending *Pending `json:"pending,omitempty"`
}

// Pending is a rotated token set, with the time the response arrived.
type Pending struct {
	AccessToken       string    `json:"access_token"`
	RefreshToken      string    `json:"refresh_token"`
	IDToken           string    `json:"id_token,omitempty"`
	Scope             string    `json:"scope,omitempty"`
	ExpiresAt         time.Time `json:"expires_at"`
	EarliestRefreshAt time.Time `json:"earliest_refresh_at,omitzero"`
	ReceivedAt        time.Time `json:"received_at"`
}

// PlanGranted reports whether the user granted ChatGPT plan usage.
func (c *Credentials) PlanGranted() bool {
	return slices.Contains(strings.Fields(c.Scope), PlanScope)
}

// OAuth talks to the token and revocation endpoints. Fields are overridable for tests.
type OAuth struct {
	TokenURL  string
	RevokeURL string
	Keys      oidc.KeySet
	HTTP      *http.Client
}

// NewOAuth returns a client for the real OpenAI endpoints.
func NewOAuth(ctx context.Context) *OAuth {
	return &OAuth{
		TokenURL:  TokenURL,
		RevokeURL: RevokeURL,
		Keys:      oidc.NewRemoteKeySet(ctx, JWKSURL),
		HTTP:      &http.Client{Timeout: 30 * time.Second},
	}
}

// TokenError is an error response from the token endpoint.
type TokenError struct {
	Status int
	Code   string
	Desc   string
}

func (e *TokenError) Error() string {
	return fmt.Sprintf("token endpoint: %d %s: %s", e.Status, e.Code, e.Desc)
}

// Terminal reports whether the refresh token is unusable and the user must log in again.
func (e *TokenError) Terminal() bool {
	switch e.Code {
	case "invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired",
		"refresh_token_invalidated", "refresh_token_reused", "invalid_client":
		return true
	}
	return false
}

type tokenResponse struct {
	AccessToken       string          `json:"access_token"`
	RefreshToken      string          `json:"refresh_token"`
	IDToken           string          `json:"id_token"`
	Scope             string          `json:"scope"`
	TokenType         string          `json:"token_type"`
	ExpiresIn         int64           `json:"expires_in"`
	EarliestRefreshAt json.RawMessage `json:"earliest_refresh_at"`
}

// Exchange trades an authorization code for tokens.
func (o *OAuth) Exchange(ctx context.Context, clientID, code, verifier, redirectURI string) (*tokenResponse, error) {
	return o.token(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {clientID},
		"code":          {code},
		"code_verifier": {verifier},
		"redirect_uri":  {redirectURI},
		"resource":      {Resource},
	}, true)
}

// Refresh rotates the refresh token and returns the new set, not yet verified.
// The old refresh token is now spent: the caller must save the result at once.
func (o *OAuth) Refresh(ctx context.Context, c *Credentials) (*Pending, error) {
	tr, err := o.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {c.ClientID},
		"refresh_token": {c.RefreshToken},
		"resource":      {Resource},
	}, false)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	return &Pending{
		AccessToken:       tr.AccessToken,
		RefreshToken:      tr.RefreshToken,
		IDToken:           tr.IDToken,
		Scope:             tr.Scope,
		ExpiresAt:         now.Add(time.Duration(tr.ExpiresIn) * time.Second),
		EarliestRefreshAt: parseTime(tr.EarliestRefreshAt),
		ReceivedAt:        now,
	}, nil
}

// ErrVerifyUnavailable means the ID token could not be checked right now (the
// signing keys are unreachable); trying again later may work.
var ErrVerifyUnavailable = errors.New("could not verify the ChatGPT sign-in: signing keys unreachable")

// ErrAccountMismatch means a refresh returned an identity other than the stored one.
var ErrAccountMismatch = errors.New("ChatGPT returned a different account than the one signed in; sign in again")

// VerifyPending checks the ID token of a rotated set, if it has one: signature,
// issuer, audience and, when subject is known, that it is the same account.
// Expiry is judged at the time the response was received.
func (o *OAuth) VerifyPending(ctx context.Context, clientID, subject string, p *Pending) error {
	if p.IDToken == "" {
		return nil
	}
	v := oidc.NewVerifier(Issuer, o.Keys, &oidc.Config{ClientID: clientID, Now: func() time.Time { return p.ReceivedAt }})
	idt, err := v.Verify(ctx, p.IDToken)
	if err != nil {
		if strings.Contains(err.Error(), "fetching keys") || ctx.Err() != nil {
			return fmt.Errorf("%w: %v", ErrVerifyUnavailable, err)
		}
		return fmt.Errorf("refreshed id token rejected: %w", err)
	}
	if subject != "" && subtle.ConstantTimeCompare([]byte(idt.Subject), []byte(subject)) != 1 {
		return ErrAccountMismatch
	}
	return nil
}

// promote makes a verified pending set the current one.
func (c *Credentials) promote(p *Pending) {
	c.AccessToken = p.AccessToken
	c.ExpiresAt = p.ExpiresAt
	c.EarliestRefreshAt = p.EarliestRefreshAt
	c.RefreshToken = p.RefreshToken
	if p.IDToken != "" {
		c.IDToken = p.IDToken
	}
	if p.Scope != "" {
		c.Scope = p.Scope
	}
	c.Pending = nil
}

func (c *Credentials) apply(tr *tokenResponse, now time.Time) {
	c.AccessToken = tr.AccessToken
	c.ExpiresAt = now.Add(time.Duration(tr.ExpiresIn) * time.Second)
	c.EarliestRefreshAt = parseTime(tr.EarliestRefreshAt)
	// Fields a refresh may omit keep their previous value.
	if tr.RefreshToken != "" {
		c.RefreshToken = tr.RefreshToken
	}
	if tr.IDToken != "" {
		c.IDToken = tr.IDToken
	}
	if tr.Scope != "" {
		c.Scope = tr.Scope
	}
}

// parseTime accepts unix seconds or RFC 3339; the docs name the field but not its format.
func parseTime(raw json.RawMessage) time.Time {
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		if secs, err := strconv.ParseInt(string(n), 10, 64); err == nil {
			return time.Unix(secs, 0)
		}
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func (o *OAuth) token(ctx context.Context, form url.Values, needIDToken bool) (*tokenResponse, error) {
	body, status, err := o.postForm(ctx, o.TokenURL, form)
	if err != nil {
		return nil, err
	}
	if status != http.StatusOK {
		return nil, parseTokenError(status, body)
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return nil, fmt.Errorf("token endpoint: %w", err)
	}
	if tr.AccessToken == "" {
		return nil, errors.New("token endpoint: response without access_token")
	}
	if err := tr.validate(needIDToken); err != nil {
		return nil, fmt.Errorf("token endpoint: %w; sign in again", err)
	}
	return &tr, nil
}

// validate rejects a token response that breaks the contract we rely on.
func (tr *tokenResponse) validate(needIDToken bool) error {
	switch {
	case tr.Scope == "":
		return errors.New("response without scope")
	case !strings.EqualFold(tr.TokenType, "bearer"):
		return fmt.Errorf("unexpected token_type %q", tr.TokenType)
	case tr.ExpiresIn <= 0:
		return errors.New("response without a positive expires_in")
	case tr.RefreshToken == "" && slices.Contains(strings.Fields(tr.Scope), "offline_access"):
		return errors.New("offline_access was granted but no refresh_token was returned")
	case needIDToken && tr.IDToken == "":
		return errors.New("response without id_token")
	}
	return nil
}

// parseTokenError accepts both {"error":"code"} (RFC 6749) and {"error":{"code":...}}.
func parseTokenError(status int, body []byte) *TokenError {
	e := &TokenError{Status: status}
	var flat struct {
		Error string `json:"error"`
		Desc  string `json:"error_description"`
	}
	if json.Unmarshal(body, &flat) == nil && flat.Error != "" {
		e.Code, e.Desc = flat.Error, flat.Desc
		return e
	}
	var nested struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &nested) == nil && nested.Error.Code != "" {
		e.Code, e.Desc = nested.Error.Code, nested.Error.Message
		return e
	}
	e.Desc = string(body)
	return e
}

var revokeRetryDelay = 300 * time.Millisecond

// Revoke revokes the refresh token. An empty 200 is success, also for already-invalid tokens.
func (o *OAuth) Revoke(ctx context.Context, c *Credentials) error {
	form := url.Values{
		"token":           {c.RefreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {c.ClientID},
	}
	body, status, err := o.postForm(ctx, o.RevokeURL, form)
	if err != nil || status >= 500 {
		// One retry for a network error or a server error.
		select {
		case <-ctx.Done():
		case <-time.After(revokeRetryDelay):
			body, status, err = o.postForm(ctx, o.RevokeURL, form)
		}
	}
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return parseTokenError(status, body)
	}
	return nil
}

func (o *OAuth) postForm(ctx context.Context, endpoint string, form url.Values) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := o.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}

// VerifyIDToken checks signature, issuer and audience (= the issued client id).
// skipExpiry is for credential import: the ID token may have expired since login,
// but its signature still proves who the record belongs to.
func (o *OAuth) VerifyIDToken(ctx context.Context, clientID, raw string, skipExpiry bool) (*oidc.IDToken, error) {
	v := oidc.NewVerifier(Issuer, o.Keys, &oidc.Config{ClientID: clientID, SkipExpiryCheck: skipExpiry})
	return v.Verify(ctx, raw)
}
