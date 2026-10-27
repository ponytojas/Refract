package main

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/users"
)

// loginAPI signs users in and out for REFRACT_AUTH=password: with a password
// (accounts made with "refract users add") or, when configured, with OIDC.
type loginAPI struct {
	users   *users.Service
	oidc    *auth.OIDC // nil without OIDC
	allowed []string   // emails and "@domains" OIDC may create accounts for; empty: existing users only
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
	oidcMux := http.NewServeMux()
	oidcMux.HandleFunc("GET /api/auth/oidc/start", a.oidcStart)
	oidcMux.HandleFunc("GET /api/auth/oidc/callback", a.oidcCallback)
	out := http.NewServeMux()
	out.Handle("/api/auth/oidc/", oidcMux)
	out.Handle("/api/auth/", auth.RequireCSRFHeader(mux))
	return out
}

// accountRoutes are for signed-in users.
func (a *loginAPI) accountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/account", a.account)
	mux.HandleFunc("POST /api/account/password", a.changePassword)
}

func (a *loginAPI) config(w http.ResponseWriter, r *http.Request) {
	name := ""
	if a.oidc != nil {
		name = a.oidc.Name
	}
	writeJSON(w, map[string]string{"oidc": name})
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
	http.Redirect(w, r, to, http.StatusFound)
}

func (a *loginAPI) oidcCallback(w http.ResponseWriter, r *http.Request) {
	if a.oidc == nil {
		http.NotFound(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Path: "/api/auth/oidc/", MaxAge: -1, HttpOnly: true, Secure: users.Secure(r)})
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
	email, err := a.oidc.Finish(r.Context(), c.Value, q.Get("state"), q.Get("code"))
	if err != nil {
		fail("failed", err)
		return
	}
	exists, err := a.users.Exists(r.Context(), email)
	if err != nil {
		fail("failed", err)
		return
	}
	if !exists && !auth.Allowed(a.allowed, email) {
		fail("not_allowed", errors.New(email))
		return
	}
	u, err := a.users.Resolve(r.Context(), email)
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
	http.Redirect(w, r, "/", http.StatusFound)
}

func (a *loginAPI) account(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	has, err := a.users.HasPassword(r.Context(), u.ID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]bool{"has_password": has})
}

// changePassword needs the current password: a stolen session alone can't
// take over the account. Other sessions are signed out.
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
