package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/usage"
	"github.com/ponytojas/Refract/internal/users"
	"github.com/ponytojas/Refract/web"
)

type adminAPI struct {
	users    *users.Service
	projects *projects.Service
	usage    *usage.Recorder
}

func (a *adminAPI) routes(rt *router) {
	rt.admin("GET /admin", web.Handler().ServeHTTP)
	rt.admin("GET /api/admin/accounts", a.accounts)
	rt.admin("GET /api/admin/accounts/report", a.report)
	rt.admin("POST /api/admin/accounts", a.manage)
}

func (a *adminAPI) accounts(w http.ResponseWriter, r *http.Request) {
	all, err := a.users.AdminAccounts(r.Context())
	if err != nil {
		accessError(w, err)
		return
	}
	writeJSON(w, all)
}

type measuredProject struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Trashed bool   `json:"trashed"`
	Bytes   int64  `json:"bytes"`
}

type accountReport struct {
	Models   []usage.ModelTotals `json:"models"`
	Projects []measuredProject   `json:"projects"`
}

func usageStart(raw string) (time.Time, error) {
	since, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || since < 0 {
		return time.Time{}, errors.New("since must be a Unix time in seconds")
	}
	return time.Unix(since, 0), nil
}

// accountUsageAndStorage is shared by the web reports and the operator CLI.
func (a *adminAPI) accountUsageAndStorage(ctx context.Context, id string, since time.Time) (accountReport, error) {
	owned, err := a.users.OwnedProjects(ctx, id)
	if err != nil {
		return accountReport{}, err
	}
	models, err := a.usage.Report(ctx, id, since)
	if err != nil {
		return accountReport{}, err
	}
	out := accountReport{Models: models, Projects: make([]measuredProject, 0, len(owned))}
	for _, p := range owned {
		size, err := a.projects.StorageBytes(ctx, p.ID)
		if err != nil {
			return accountReport{}, err
		}
		out.Projects = append(out.Projects, measuredProject{ID: p.ID, Name: p.Name, Trashed: p.Trashed, Bytes: size})
	}
	return out, nil
}

func (a *adminAPI) report(w http.ResponseWriter, r *http.Request) {
	since, err := usageStart(r.URL.Query().Get("since"))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	id := r.URL.Query().Get("id")
	if _, err := a.users.AccountByID(r.Context(), id); err != nil {
		adminError(w, err)
		return
	}
	out, err := a.accountUsageAndStorage(r.Context(), id, since)
	if err != nil {
		accessError(w, err)
		return
	}
	writeJSON(w, out)
}

type adminAction struct {
	Action    string `json:"action"`
	ID        string `json:"id"`
	Email     string `json:"email"`
	Confirmed bool   `json:"confirmed"`
	Password  string `json:"password,omitempty"`
}

type adminResult struct {
	Password string `json:"password,omitempty"`
	Count    int64  `json:"count,omitempty"`
}

var (
	errAdminAction       = errors.New("unknown account action")
	errAdminConfirmation = errors.New("confirm this action first")
	errAdminAddress      = errors.New("enter a valid email address")
	errAdminReauth       = errors.New("reauthentication response already written")
)

func (a *adminAPI) manage(w http.ResponseWriter, r *http.Request) {
	var in adminAction
	if !decode(w, r, &in) {
		return
	}
	switch in.Action {
	case "add", "reset", "rename", "signout", "unlock", "unlock_all", "remove":
	default:
		adminError(w, errAdminAction)
		return
	}
	reauthenticate := func() error {
		actor, err := a.users.AccountByID(r.Context(), requestUser(r.Context()))
		if err != nil {
			return err
		}
		if !requireAccountReauth(a.users, w, r, actor, in.Password) {
			return errAdminReauth
		}
		return nil
	}
	out, target, err := a.apply(r.Context(), in, reauthenticate)
	if errors.Is(err, errAdminReauth) {
		return
	}
	partial := err != nil && in.Action == "remove" && out.Count > 0
	result := "success"
	if err != nil {
		result = "failed"
	}
	if partial {
		result = "partial"
	}
	slog.Info("account administration", "actor", requestUser(r.Context()), "target", target, "action", in.Action, "result", result, "affected", out.Count)
	if partial {
		partialAccountRemoval(w, out.Count, err)
		return
	}
	if err != nil {
		adminError(w, err)
		return
	}
	writeJSON(w, out)
}

func (a *adminAPI) apply(ctx context.Context, in adminAction, reauthenticate func() error) (adminResult, string, error) {
	var out adminResult
	if in.Action == "add" || in.Action == "rename" {
		if err := users.ValidateEmail(in.Email); err != nil {
			return out, "", errAdminAddress
		}
	}
	if in.Action == "add" {
		u, password, err := a.users.ProvisionPassword(ctx, in.Email, true)
		out.Password = password
		return out, u.ID, err
	}
	if !in.Confirmed {
		return out, "", errAdminConfirmation
	}
	if in.Action == "unlock_all" {
		count, err := a.users.UnlockAll(ctx)
		out.Count = count
		return out, "", err
	}

	acct, err := a.users.AccountByID(ctx, in.ID)
	if err != nil {
		return out, "", err
	}
	if in.Action == "reset" || in.Action == "rename" {
		if err := reauthenticate(); err != nil {
			return out, acct.ID, err
		}
	}
	switch in.Action {
	case "reset":
		out.Password, err = a.users.ResetAccountPassword(ctx, acct.ID)
	case "rename":
		err = a.users.RenameAccount(ctx, acct.ID, in.Email)
	case "signout":
		out.Count, err = a.users.SignOutAccount(ctx, acct.ID)
	case "unlock":
		var cleared bool
		cleared, err = a.users.Unlock(ctx, acct.Email)
		if cleared {
			out.Count = 1
		}
	case "remove":
		var deleted int
		deleted, err = a.users.RemoveKeepingAdmin(context.WithoutCancel(ctx), acct.ID, a.projects.DestroyOwnedProjects, reauthenticate)
		out.Count = int64(deleted)
	}
	return out, acct.ID, err
}

func adminError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, users.ErrNoAccount):
		httpError(w, err, http.StatusNotFound)
	case errors.Is(err, errAdminConfirmation), errors.Is(err, errAdminAction):
		httpError(w, err, http.StatusBadRequest)
	case errors.Is(err, errAdminAddress):
		kindError(w, http.StatusBadRequest, "admin_invalid_email", err)
	case errors.Is(err, users.ErrAccountExists):
		kindError(w, http.StatusConflict, "admin_account_exists", err)
	case errors.Is(err, users.ErrLastAdmin):
		kindError(w, http.StatusConflict, "last_admin", err)
	case errors.Is(err, users.ErrOwnsProjects), errors.Is(err, projects.ErrTurnRunning):
		kindError(w, http.StatusConflict, "admin_account_busy", errors.New("the account's projects are changing"))
	default:
		accessError(w, err)
	}
}
