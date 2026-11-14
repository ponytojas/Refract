package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mail"
	"github.com/ponytojas/Refract/internal/users"
)

// The account lifecycle: sign-up, confirming an address, choosing a new
// password and deleting the account. The routes before sign-in answer the same
// whether or not an address has an account, and send their email from the
// outbox after answering, so neither the answer nor its timing tells.

const oidcThenCookie = "refract_oidc_then"

func (a *loginAPI) mailReady() bool { return a.mail != nil && a.public != "" }

func decode(w http.ResponseWriter, r *http.Request, in any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(in); err != nil && !errors.Is(err, io.EOF) { // an empty body asks for nothing
		httpError(w, err, http.StatusBadRequest)
		return false
	}
	return true
}

func (a *loginAPI) config(w http.ResponseWriter, r *http.Request) {
	name := ""
	if a.oidc != nil {
		name = a.oidc.Name
	}
	writeJSON(w, map[string]any{"oidc": name, "mode": string(a.mode), "signup": a.signup, "reset": a.mailReady()})
}

// allowMail answers 503 or 429 when an email can't be sent for email now.
func (a *loginAPI) allowMail(w http.ResponseWriter, r *http.Request, email string) bool {
	if !a.mailReady() {
		kindError(w, http.StatusServiceUnavailable, "mail_unavailable", errors.New("this server can't send emails"))
		return false
	}
	switch err := a.users.AllowMail(r.Context(), email, users.ClientIP(r)); {
	case errors.Is(err, users.ErrTooManyMails):
		kindError(w, http.StatusTooManyRequests, "too_many_emails", err)
		return false
	case err != nil:
		httpError(w, err, http.StatusInternalServerError)
		return false
	}
	return true
}

// send composes the email for a link and queues it.
func (a *loginAPI) send(r *http.Request, l users.Link) {
	kind, path := mail.Verify, "/verify#token="
	if l.Purpose == users.Reset {
		kind, path = mail.Reset, "/reset#token="
	}
	a.compose(r, kind, l.To, mail.Data{Link: a.public + path + l.Token, SignUp: l.SignUp})
}

func (a *loginAPI) compose(r *http.Request, kind mail.Kind, to users.Account, d mail.Data) {
	d.Email = to.Email
	if u, err := url.Parse(a.public); err == nil {
		d.Site = u.Host
	}
	m, err := mail.Compose(kind, cmp.Or(to.Lang, users.RequestLang(r), "en"), to.Email, d)
	if err != nil {
		slog.Error("composing an email", "kind", kind, "err", err)
		return
	}
	a.mail.Send(m)
}

func (a *loginAPI) signUp(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email, Password, Lang string }
	if !decode(w, r, &in) {
		return
	}
	if err := users.ValidateSignUp(in.Email, in.Password, in.Lang); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if !a.signup {
		kindError(w, http.StatusForbidden, "signup_closed", errors.New("this server doesn't take new accounts"))
		return
	}
	if !a.allowMail(w, r, in.Email) {
		return
	}
	l, err := a.users.SignUp(r.Context(), in.Email, in.Password, in.Lang)
	if err != nil {
		slog.Error("sign-up", "err", err)
		httpError(w, errors.New("internal error"), http.StatusInternalServerError)
		return
	}
	a.send(r, l)
	w.WriteHeader(http.StatusNoContent)
}

func (a *loginAPI) forgot(w http.ResponseWriter, r *http.Request) {
	var in struct{ Email string }
	if !decode(w, r, &in) {
		return
	}
	if err := users.ValidateEmail(in.Email); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if !a.allowMail(w, r, in.Email) {
		return
	}
	l, ok, err := a.users.RequestReset(r.Context(), in.Email)
	if err != nil {
		slog.Error("reset request", "err", err)
		httpError(w, errors.New("internal error"), http.StatusInternalServerError)
		return
	}
	if ok {
		a.send(r, l)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *loginAPI) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if !decode(w, r, &in) {
		return
	}
	u, err := a.users.ResetPassword(r.Context(), in.Token, in.Password)
	switch {
	case errors.Is(err, users.ErrBadToken):
		kindError(w, http.StatusBadRequest, "invalid_token", err)
	case err != nil && users.ValidatePassword(in.Password) != nil:
		httpError(w, err, http.StatusBadRequest)
	case err != nil:
		httpError(w, err, http.StatusInternalServerError)
	default:
		a.signIn(w, r, u.ID)
	}
}

func (a *loginAPI) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if !decode(w, r, &in) {
		return
	}
	var signedInAs string
	if c, err := r.Cookie(users.SessionCookie); err == nil {
		if u, err := a.users.Session(r.Context(), c.Value); err == nil {
			signedInAs = u.ID
		}
	}
	u, err := a.users.VerifyEmail(r.Context(), in.Token, signedInAs, in.Password, users.ClientIP(r))
	switch {
	case errors.Is(err, users.ErrBadToken):
		kindError(w, http.StatusBadRequest, "invalid_token", err)
	case errors.Is(err, users.ErrPasswordRequired):
		kindError(w, http.StatusUnauthorized, "password_required", err)
	case errors.Is(err, users.ErrBadLogin):
		kindError(w, http.StatusUnauthorized, "bad_login", err)
	case errors.Is(err, users.ErrTooMany):
		kindError(w, http.StatusTooManyRequests, "too_many_attempts", err)
	case err != nil:
		httpError(w, err, http.StatusInternalServerError)
	case u.ID == signedInAs:
		w.WriteHeader(http.StatusNoContent)
	default:
		a.signIn(w, r, u.ID)
	}
}

// callbackTarget is where the provider's sign-in returns to: Settings → Account
// when it was asked for to confirm deleting the account.
func callbackTarget(r *http.Request) string {
	if c, err := r.Cookie(oidcThenCookie); err == nil && c.Value == "account" {
		return "/settings/account"
	}
	return "/"
}

// Signed-in account routes.

func (a *loginAPI) account(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	acct, err := a.users.AccountByID(r.Context(), u.ID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	owned, err := a.users.OwnedProjects(r.Context(), u.ID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	name := ""
	if a.oidc != nil {
		name = a.oidc.Name
	}
	writeJSON(w, map[string]any{
		"email": acct.Email, "has_password": acct.HasPassword, "email_verified": acct.Verified, "lang": acct.Lang,
		"projects": len(owned), "mail": a.mailReady(), "oidc": name, "recent_sign_in": a.recentSignIn(r),
	})
}

// recentSignIn: this session's sign-in happened within users.RecentAuth.
func (a *loginAPI) recentSignIn(r *http.Request) bool {
	c, err := r.Cookie(users.SessionCookie)
	if err != nil {
		return false
	}
	at, err := a.users.AuthenticatedAt(r.Context(), c.Value)
	return err == nil && !at.IsZero() && time.Since(at) <= users.RecentAuth
}

func (a *loginAPI) resendVerification(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if !a.allowMail(w, r, u.Email) {
		return
	}
	l, ok, err := a.users.ResendVerification(r.Context(), u.ID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if ok {
		a.send(r, l)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *loginAPI) setLang(w http.ResponseWriter, r *http.Request) {
	var in struct{ Lang string }
	if !decode(w, r, &in) {
		return
	}
	u, _ := auth.UserFrom(r.Context())
	if err := a.users.SetLang(r.Context(), u.ID, in.Lang); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// deleteAccount removes the account and everything it owns, trash included. It
// asks for the password, or, for an account without one, a sign-in in the last
// few minutes.
func (a *loginAPI) deleteAccount(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if !decode(w, r, &in) {
		return
	}
	u, _ := auth.UserFrom(r.Context())
	acct, err := a.users.AccountByID(r.Context(), u.ID)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if acct.HasPassword {
		switch _, err := a.users.Login(r.Context(), acct.Email, in.Password, users.ClientIP(r)); {
		case errors.Is(err, users.ErrTooMany):
			kindError(w, http.StatusTooManyRequests, "too_many_attempts", err)
			return
		case err != nil:
			kindError(w, http.StatusForbidden, "wrong_password", errors.New("the password is wrong"))
			return
		}
	} else if !a.recentSignIn(r) {
		kindError(w, http.StatusForbidden, "reauth_required", errors.New("sign in again to delete your account"))
		return
	}
	n, err := a.users.Remove(r.Context(), u.ID, a.destroy)
	switch {
	case errors.Is(err, users.ErrOwnsProjects):
		httpError(w, errors.New("a project was created meanwhile: try again"), http.StatusConflict)
		return
	case err != nil:
		projectError(w, err)
		return
	}
	if a.mailReady() {
		a.compose(r, mail.Deleted, acct, mail.Data{})
	}
	users.ClearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
	slog.Info("account deleted", "user", u.ID, "projects", n)
}

// thenAccount is the only value oidcStart takes for ?then=.
func thenAccount(r *http.Request) bool { return r.URL.Query().Get("then") == "account" }
