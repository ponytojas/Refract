package main

import (
	"cmp"
	"context"
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

// allowMail answers 503 or 429 (ok is false) when an email can't be asked for
// now. When the address has had its share, send is false and the caller
// answers as if it had sent: only the network address's limit is told.
func (a *loginAPI) allowMail(w http.ResponseWriter, r *http.Request, email string) (send, ok bool) {
	if !a.mailReady() {
		kindError(w, http.StatusServiceUnavailable, "mail_unavailable", errors.New("this server can't send emails"))
		return false, false
	}
	send, err := a.users.AllowMail(r.Context(), email, users.ClientIP(r))
	switch {
	case errors.Is(err, users.ErrTooManyMails):
		kindError(w, http.StatusTooManyRequests, "too_many_emails", err)
		return false, false
	case err != nil:
		httpError(w, err, http.StatusInternalServerError)
		return false, false
	}
	return send, true
}

// later runs job after the answer, on the outbox's worker. Everything that
// depends on whether an address has an account happens there, so a request
// takes the same path and time either way.
func (a *loginAPI) later(r *http.Request, job func(ctx context.Context, lang string)) {
	lang := users.RequestLang(r)
	a.mail.Run(func(ctx context.Context) { job(ctx, lang) })
}

// send composes the email for a link and delivers it from inside a job on the
// outbox's worker (so draining waits for it). lang is the interface language
// of the request, for an account with none of its own.
func (a *loginAPI) send(ctx context.Context, lang string, l users.Link) {
	kind, path := mail.Verify, "/verify#token="
	if l.Purpose == users.Reset {
		kind, path = mail.Reset, "/reset#token="
	}
	if m, ok := a.compose(lang, kind, l.To, mail.Data{Link: a.public + path + l.Token, SignUp: l.SignUp}); ok {
		a.mail.Deliver(ctx, m)
	}
}

func (a *loginAPI) compose(lang string, kind mail.Kind, to users.Account, d mail.Data) (mail.Message, bool) {
	d.Email = to.Email
	if u, err := url.Parse(a.public); err == nil {
		d.Site = u.Host
	}
	m, err := mail.Compose(kind, cmp.Or(to.Lang, lang, "en"), to.Email, d)
	if err != nil {
		slog.Error("composing an email", "kind", kind, "err", err)
		return mail.Message{}, false
	}
	return m, true
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
	if send, ok := a.allowMail(w, r, in.Email); !ok {
		return
	} else if send {
		a.later(r, func(ctx context.Context, lang string) {
			l, err := a.users.SignUp(ctx, in.Email, in.Password, in.Lang)
			if err != nil {
				slog.Error("sign-up", "err", err)
				return
			}
			a.send(ctx, lang, l)
		})
	}
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
	if send, ok := a.allowMail(w, r, in.Email); !ok {
		return
	} else if send {
		a.later(r, func(ctx context.Context, lang string) {
			l, ok, err := a.users.RequestReset(ctx, in.Email)
			if err != nil {
				slog.Error("reset request", "err", err)
			} else if ok {
				a.send(ctx, lang, l)
			}
		})
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *loginAPI) resetPassword(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if !decode(w, r, &in) {
		return
	}
	if err := a.users.AllowReset(r.Context(), users.ClientIP(r)); err != nil {
		if errors.Is(err, users.ErrTooManyResets) {
			kindError(w, http.StatusTooManyRequests, "too_many_attempts", err)
		} else {
			httpError(w, err, http.StatusInternalServerError)
		}
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
		a.signIn(w, r, u)
	}
}

func (a *loginAPI) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var in struct{ Token, Password string }
	if !decode(w, r, &in) {
		return
	}
	var signedInAs string
	if u, _, err := a.users.SessionFrom(r.Context(), r); err == nil {
		signedInAs = u.ID
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
		a.signIn(w, r, u)
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
	owned, err := a.users.OwnedCount(r.Context(), u.ID)
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
		"projects": owned, "mail": a.mailReady(), "oidc": name, "recent_sign_in": a.recentSignIn(r),
	})
}

// recentSignIn: this session's sign-in happened within users.RecentAuth.
func (a *loginAPI) recentSignIn(r *http.Request) bool {
	_, token, err := a.users.SessionFrom(r.Context(), r)
	if err != nil {
		return false
	}
	at, err := a.users.AuthenticatedAt(r.Context(), token)
	return err == nil && !at.IsZero() && time.Since(at) <= users.RecentAuth
}

func (a *loginAPI) resendVerification(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	if send, ok := a.allowMail(w, r, u.Email); !ok {
		return
	} else if send {
		a.later(r, func(ctx context.Context, lang string) {
			l, ok, err := a.users.ResendVerification(ctx, u.ID)
			if err != nil {
				slog.Error("resending a verification link", "err", err)
			} else if ok {
				a.send(ctx, lang, l)
			}
		})
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
	// Once started, deleting runs to the end even if the client goes away.
	n, err := a.users.Remove(context.WithoutCancel(r.Context()), u.ID, a.destroy)
	switch {
	case err != nil && n > 0:
		partialAccountRemoval(w, int64(n), err)
		return
	case errors.Is(err, users.ErrOwnsProjects):
		httpError(w, errors.New("a project was created meanwhile: try again"), http.StatusConflict)
		return
	case err != nil:
		projectError(w, err)
		return
	}
	if a.mailReady() {
		if m, ok := a.compose(users.RequestLang(r), mail.Deleted, acct, mail.Data{}); ok {
			a.mail.Send(m)
		}
	}
	users.ClearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
	slog.Info("account deleted", "user", u.ID, "projects", n)
}

func partialAccountRemoval(w http.ResponseWriter, removed int64, err error) {
	slog.Warn("account removal stopped after deleting projects", "removed_projects", removed, "err", err)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusConflict)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{
		"kind": "account_removal_partial", "message": "some projects were deleted before account removal stopped", "removed_projects": removed,
	}})
}

// thenAccount is the only value oidcStart takes for ?then=.
func thenAccount(r *http.Request) bool { return r.URL.Query().Get("then") == "account" }

// Sessions: the account's signed-in browsers, and ending them.

func (a *loginAPI) sessions(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	list, err := a.users.Sessions(r.Context(), u.ID, users.CurrentSession(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}

// endSession ends one of the caller's sessions; ending the current one signs
// this browser out too.
func (a *loginAPI) endSession(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	id := r.PathValue("id")
	switch err := a.users.RevokeSession(r.Context(), u.ID, id); {
	case errors.Is(err, users.ErrNoSession):
		kindError(w, http.StatusNotFound, "not_found", err)
		return
	case err != nil:
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if id == users.CurrentSession(r.Context()) {
		users.ClearSessionCookie(w, r)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *loginAPI) endOtherSessions(w http.ResponseWriter, r *http.Request) {
	u, _ := auth.UserFrom(r.Context())
	n, err := a.users.RevokeOtherSessions(r.Context(), u.ID, users.CurrentSession(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]int64{"ended": n})
}
