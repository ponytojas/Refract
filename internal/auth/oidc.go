package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"
	"sync"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDC signs users in with an OpenID Connect provider (Google, Microsoft,
// Keycloak, Authentik, Authelia…): authorization code flow with PKCE, the ID
// token verified against the provider's keys, and its verified email as the
// identity.
type OIDC struct {
	Name         string // shown on the sign-in button
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string // <public URL>/api/auth/oidc/callback

	mu       sync.Mutex
	provider *oidc.Provider
}

// discover fetches the provider's configuration once it answers.
func (o *OIDC) discover(ctx context.Context) (*oidc.Provider, *oauth2.Config, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.provider == nil {
		// Not the request's context: the provider keeps it to refresh its keys.
		p, err := oidc.NewProvider(context.WithoutCancel(ctx), o.Issuer)
		if err != nil {
			return nil, nil, err
		}
		o.provider = p
	}
	return o.provider, &oauth2.Config{
		ClientID:     o.ClientID,
		ClientSecret: o.ClientSecret,
		Endpoint:     o.provider.Endpoint(),
		RedirectURL:  o.RedirectURL,
		Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
	}, nil
}

func random() string {
	b := make([]byte, 24)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// Start returns where to send the browser, and a value to keep in a cookie
// until it comes back (state, nonce and PKCE verifier).
func (o *OIDC) Start(ctx context.Context) (redirect, cookie string, err error) {
	_, cfg, err := o.discover(ctx)
	if err != nil {
		return "", "", err
	}
	state, nonce, verifier := random(), random(), oauth2.GenerateVerifier()
	return cfg.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), state + "." + nonce + "." + verifier, nil
}

// Finish checks the provider's answer against the cookie from Start and
// returns the signed-in email, and whether the provider vouched for it
// (email_verified present and true). A claim that is present and false is refused.
func (o *OIDC) Finish(ctx context.Context, cookie, state, code string) (email string, verified bool, err error) {
	parts := strings.SplitN(cookie, ".", 3)
	if len(parts) != 3 || state == "" || subtle.ConstantTimeCompare([]byte(parts[0]), []byte(state)) != 1 {
		return "", false, errors.New("the sign-in expired or came from another browser: try again")
	}
	p, cfg, err := o.discover(ctx)
	if err != nil {
		return "", false, err
	}
	tok, err := cfg.Exchange(ctx, code, oauth2.VerifierOption(parts[2]))
	if err != nil {
		return "", false, err
	}
	raw, _ := tok.Extra("id_token").(string)
	if raw == "" {
		return "", false, errors.New("the provider sent no ID token")
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: o.ClientID}).Verify(ctx, raw)
	if err != nil {
		return "", false, err
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(parts[1])) != 1 {
		return "", false, errors.New("the ID token is not for this sign-in")
	}
	var claims struct {
		Email    string `json:"email"`
		Verified *bool  `json:"email_verified"`
	}
	if err := idt.Claims(&claims); err != nil {
		return "", false, err
	}
	if claims.Email == "" {
		return "", false, errors.New("the provider shared no email address")
	}
	if claims.Verified != nil && !*claims.Verified {
		return "", false, errors.New("the email address is not verified with the provider")
	}
	return strings.ToLower(claims.Email), claims.Verified != nil && *claims.Verified, nil
}

// Allowed reports whether email matches a list of addresses and "@domain"
// entries.
func Allowed(list []string, email string) bool {
	for _, a := range list {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && (a == email || strings.HasPrefix(a, "@") && strings.HasSuffix(email, a)) {
			return true
		}
	}
	return false
}
