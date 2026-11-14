package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mail"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/users"
)

// loginAPI signs users in and out for REFRACT_AUTH=builtin: with a password
// (accounts made with "refract users add") or, when configured, with OIDC.
type loginAPI struct {
	users   *users.Service
	oidc    *auth.OIDC // nil without OIDC
	allowed []string   // emails and "@domains" OIDC may create accounts for; empty: existing users only

	mode    mode.Mode                                         // REFRACT_MODE
	signup  bool                                              // people may create their own account (open sign-up, and mail to confirm it)
	public  string                                            // REFRACT_PUBLIC_URL, the base of links in emails; "" without
	mail    *mail.Outbox                                      // nil when the server can't send email
	destroy func(ctx context.Context, projectID string) error // deletes a project for good, for removing an account
}

const oidcCookie = "refract_oidc"

// handler serves /api/auth/: outside the session guard, since nobody is signed
// in yet. The OIDC routes are browser navigations, which can't carry the CSRF
// header; the state cookie and PKCE protect them instead.
func (a *loginAPI) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/config", a.config)
	mux.HandleFunc("POST /api/auth/login", a.login)
	mux.HandleFunc("POST /api/auth/logout", a.logout)
	// Served to people who aren't signed in yet: they ask for sign-up, a new
	// password or the confirmation of an address.
	mux.HandleFunc("POST /api/auth/signup", a.signUp)
	mux.HandleFunc("POST /api/auth/forgot", a.forgot)
	mux.HandleFunc("POST /api/auth/reset", a.resetPassword)
	mux.HandleFunc("POST /api/auth/verify", a.verifyEmail)
	oidcMux := http.NewServeMux()
	oidcMux.HandleFunc("GET /api/auth/oidc/start", a.oidcStart)
	oidcMux.HandleFunc("GET /api/auth/oidc/callback", a.oidcCallback)
	out := http.NewServeMux()
	out.Handle("/api/auth/oidc/", oidcMux)
	out.Handle("/api/auth/", auth.RequireCSRFHeader(mux))
	return out
}

// accountRoutes are for signed-in users.
func (a *loginAPI) accountRoutes(rt *router) {
	rt.user("GET /api/account", a.account)
	rt.user("POST /api/account/password", a.changePassword)
	rt.user("POST /api/account/verify-email", a.resendVerification)
	rt.user("PUT /api/account/lang", a.setLang)
	rt.user("POST /api/account/delete", a.deleteAccount)
}

func (a *loginAPI) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	u, err := a.users.Login(r.Context(), in.Email, in.Password, users.ClientIP(r))
	switch {
	case errors.Is(err, users.ErrTooMany):
		kindError(w, http.StatusTooManyRequests, "too_many_attempts", err)
		return
	case errors.Is(err, users.ErrBadLogin):
		kindError(w, http.StatusUnauthorized, "bad_login", err)
		return
	case err != nil:
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	a.signIn(w, r, u.ID)
}

func (a *loginAPI) signIn(w http.ResponseWriter, r *http.Request, userID string) bool {
	token, err := a.users.NewSession(r.Context(), userID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return false
	}
	users.SetSessionCookie(w, r, token)
	w.WriteHeader(http.StatusNoContent)
	return true
}

func (a *loginAPI) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(users.SessionCookie); err == nil {
		a.users.EndSession(r.Context(), c.Value)
	}
	users.ClearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

func (a *loginAPI) oidcStart(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.NotFound(w, r)
		return
	}
	to, cookie, err := a.oidc.Start(r.Context())
	if err != nil {
		slog.Warn("oidc discovery failed", "err", err)
		http.Redirect(w, r, "/?login_error=failed", http.StatusFound)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: cookie, Path: "/api/auth/oidc/", MaxAge: 600,
		HttpOnly: true, Secure: users.Secure(r), SameSite: http.SameSiteLaxMode})
	then := &http.Cookie{Name: oidcThenCookie, Path: "/api/auth/oidc/", HttpOnly: true, Secure: users.Secure(r), SameSite: http.SameSiteLaxMode}
	if thenAccount(r) { // back to Settings → Account afterwards, to finish deleting the account
		then.Value, then.MaxAge = "account", 600
	} else { // a plain sign-in must not inherit an earlier, unfinished one
		then.MaxAge = -1
	}
	http.SetCookie(w, then)
	http.Redirect(w, r, to, http.StatusFound)
}

func (a *loginAPI) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Path: "/api/auth/oidc/", MaxAge: -1, HttpOnly: true, Secure: users.Secure(r)})
	http.SetCookie(w, &http.Cookie{Name: oidcThenCookie, Path: "/api/auth/oidc/", MaxAge: -1, HttpOnly: true, Secure: users.Secure(r)})
	target := callbackTarget(r)
	fail := func(code string, err error) {
		slog.Warn("oidc sign-in refused", "reason", code, "err", err)
		http.Redirect(w, r, "/?login_error="+url.QueryEscape(code), http.StatusFound)
	}
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		fail("expired", err)
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		fail("failed", errors.New(e))
		return
	}
	email, verified, err := a.oidc.Finish(r.Context(), c.Value, q.Get("state"), q.Get("code"))
	if err != nil {
		fail("failed", err)
		return
	}
	u, refusal, err := a.admitOIDC(r.Context(), email, verified)
	if refusal != "" {
		fail(refusal, errors.New(email))
		return
	}
	if err != nil {
		fail("failed", err)
		return
	}
	token, err := a.users.NewSession(r.Context(), u.ID)
	if err != nil {
		fail("failed", err)
		return
	}
	users.SetSessionCookie(w, r, token)
	slog.Info("signed in with oidc", "user", u.ID)
	http.Redirect(w, r, target, http.StatusFound)
}

// admitOIDC decides whether a provider sign-in may proceed, and into which
// account. refusal is the login_error code when it may not. verified is
// whether the provider vouched for the address (email_verified true); the
// provider's claim being false never gets here, oidc.Finish refuses it.
//
// Signing in by address alone is only safe when the account can't be someone
// else's. An account Refract has verified (made by the operator, by a provider
// or confirmed by its owner) can't be a stranger's, so a provider that doesn't
// send the claim (Microsoft Entra) may sign in to it. An account never
// confirmed started as a sign-up: it needs the provider's word, and then the
// provider takes it over, wiping what the sign-up attached (ConfirmByProvider).
func (a *loginAPI) admitOIDC(ctx context.Context, email string, verified bool) (u auth.User, refusal string, err error) {
	acct, err := a.users.Lookup(ctx, email)
	switch {
	case errors.Is(err, users.ErrNoAccount):
		if !auth.Allowed(a.allowed, email) {
			return auth.User{}, "not_allowed", nil
		}
		u, err = a.users.Resolve(ctx, email) // an account made for an identity the operator allowed: verified
		return u, "", err
	case err != nil:
		return auth.User{}, "", err
	}
	if !acct.Verified {
		if !verified {
			return auth.User{}, "unverified_email", nil
		}
		if err := a.users.ConfirmByProvider(ctx, acct.ID); err != nil {
			return auth.User{}, "", err
		}
	}
	return auth.User{ID: acct.ID, Email: acct.Email}, "", nil
}

func (a *loginAPI) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	u, _ := auth.UserFrom(r.Context())
	if _, err := a.users.Login(r.Context(), u.Email, in.Current, users.ClientIP(r)); err != nil {
		kindError(w, http.StatusForbidden, "wrong_password", errors.New("the current password is wrong"))
		return
	}
	if _, err := a.users.SetPassword(r.Context(), u.Email, in.New); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	a.signIn(w, r, u.ID)
}

func kindError(w http.ResponseWriter, status int, kind string, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"kind": kind, "message": err.Error()}})
}
