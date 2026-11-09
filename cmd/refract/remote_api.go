package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/ponytojas/Refract/internal/access"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/github"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

// remoteAPI: a project's git remote (push, pull, auto-push) and the GitHub account.
type remoteAPI struct {
	svc    *projects.Service
	db     *sql.DB
	key    []byte
	github *github.Client // nil when REFRACT_GITHUB_CLIENT_ID is not set

	mu      sync.Mutex
	last    map[string]syncStatus         // project → last push/pull
	devices map[string]*github.DeviceCode // user → their sign-in in progress
}

type syncStatus struct {
	Op    string    `json:"op"` // push | pull
	At    time.Time `json:"at"`
	Auto  bool      `json:"auto"`
	Error string    `json:"error,omitempty"`
}

func (a *remoteAPI) routes(rt *router) {
	rt.project("GET /api/projects/{id}/remote", access.Owner, a.get)
	rt.project("PUT /api/projects/{id}/remote", access.Owner, a.put)
	rt.project("POST /api/projects/{id}/remote/push", access.Owner, a.push)
	rt.project("POST /api/projects/{id}/remote/pull", access.Owner, a.pull)
	rt.user("GET /api/github", a.githubStatus)
	rt.user("POST /api/github/device", a.githubStart)
	rt.user("POST /api/github/device/poll", a.githubPoll)
	rt.user("DELETE /api/github", a.githubDisconnect)
	rt.user("GET /api/github/repos", a.githubRepos)
	rt.user("POST /api/github/repos", a.githubCreate)
}

func (a *remoteAPI) record(id, op string, auto bool, err error) syncStatus {
	st := syncStatus{Op: op, At: time.Now(), Auto: auto}
	if err != nil {
		st.Error = err.Error()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.last == nil {
		a.last = map[string]syncStatus{}
	}
	a.last[id] = st
	return st
}

// AutoPush runs after commits (projects.Service.OnCommit).
func (a *remoteAPI) AutoPush(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	r, err := a.svc.Remote(ctx, id)
	if err != nil || !r.AutoPush {
		return
	}
	err = a.svc.Push(ctx, id)
	a.record(id, "push", true, err)
	if err != nil {
		slog.Warn("auto-push failed", "project", id, "err", err)
	}
}

func (a *remoteAPI) get(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	out := map[string]any{"url": "", "branch": "main", "auto_push": false, "github": false, "has_token": false}
	rem, err := a.svc.Remote(r.Context(), id)
	switch {
	case errors.Is(err, projects.ErrNoRemote):
	case err != nil:
		projectError(w, err)
		return
	default:
		out = map[string]any{"url": rem.URL, "branch": rem.Branch, "auto_push": rem.AutoPush, "github": rem.GitHub, "has_token": rem.Token != ""}
	}
	a.mu.Lock()
	if st, ok := a.last[id]; ok {
		out["last"] = st
	}
	a.mu.Unlock()
	writeJSON(w, out)
}

func (a *remoteAPI) put(w http.ResponseWriter, r *http.Request) {
	var in struct {
		projects.Remote
		ClearToken bool `json:"clear_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := a.svc.SetRemote(r.Context(), r.PathValue("id"), in.Remote, !in.ClearToken); err != nil {
		projectError(w, err)
		return
	}
	a.get(w, r)
}

func (a *remoteAPI) push(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	err := a.svc.Push(r.Context(), id)
	st := a.record(id, "push", false, err)
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, st)
}

func (a *remoteAPI) pull(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	res, err := a.svc.Pull(r.Context(), id)
	a.record(id, "pull", false, err)
	if err != nil {
		projectError(w, err)
		return
	}
	writeJSON(w, res)
}

// --- GitHub account

type githubAccount struct {
	Token string `json:"token"`
	Login string `json:"login"`
}

// githubKey is a user's GitHub account setting (sealed, bound to that user).
func githubKey(user string) string { return store.UserKey(user, "github") }

func requestUser(ctx context.Context) string {
	u, _ := auth.UserFrom(ctx)
	return u.ID
}

func (a *remoteAPI) account(ctx context.Context, user string) (*githubAccount, error) {
	sealed, err := store.GetSetting(ctx, a.db, githubKey(user))
	if err != nil || sealed == "" {
		return nil, err
	}
	plain, err := store.Unseal(a.key, sealed, githubKey(user))
	if err != nil {
		return nil, err
	}
	var acc githubAccount
	return &acc, json.Unmarshal(plain, &acc)
}

// Token is projects.Service.GitHubToken: the token of a project's owner.
func (a *remoteAPI) Token(ctx context.Context, owner string) (string, error) {
	acc, err := a.account(ctx, owner)
	if err != nil {
		return "", err
	}
	if acc == nil {
		return "", errors.New("connect a GitHub account in Settings first")
	}
	return acc.Token, nil
}

func (a *remoteAPI) githubStatus(w http.ResponseWriter, r *http.Request) {
	acc, err := a.account(r.Context(), requestUser(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	out := map[string]any{"configured": a.github != nil, "connected": acc != nil}
	if acc != nil {
		out["login"] = acc.Login
	}
	writeJSON(w, out)
}

func (a *remoteAPI) githubStart(w http.ResponseWriter, r *http.Request) {
	if a.github == nil {
		httpError(w, errors.New("set REFRACT_GITHUB_CLIENT_ID to connect GitHub"), http.StatusNotImplemented)
		return
	}
	d, err := a.github.StartDevice(r.Context())
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	a.mu.Lock()
	if a.devices == nil {
		a.devices = map[string]*github.DeviceCode{}
	}
	a.devices[requestUser(r.Context())] = d
	a.mu.Unlock()
	// The device code stays on the server; the browser only shows the user code.
	writeJSON(w, map[string]any{"user_code": d.UserCode, "verification_uri": d.VerificationURI, "interval": d.Interval, "expires_in": d.ExpiresIn})
}

func (a *remoteAPI) githubPoll(w http.ResponseWriter, r *http.Request) {
	user := requestUser(r.Context())
	a.mu.Lock()
	d := a.devices[user]
	a.mu.Unlock()
	if d == nil || a.github == nil {
		writeJSON(w, map[string]string{"status": "expired"})
		return
	}
	token, err := a.github.PollDevice(r.Context(), d.DeviceCode)
	switch {
	case errors.Is(err, github.ErrPending):
		writeJSON(w, map[string]string{"status": "pending"})
		return
	case errors.Is(err, github.ErrExpired), errors.Is(err, github.ErrDenied):
		writeJSON(w, map[string]string{"status": "failed", "error": err.Error()})
		return
	case err != nil:
		httpError(w, err, http.StatusBadGateway)
		return
	}
	u, err := a.github.User(r.Context(), token)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	plain, _ := json.Marshal(githubAccount{Token: token, Login: u.Login})
	sealed, err := store.Seal(a.key, plain, githubKey(user))
	if err == nil {
		err = store.SetSetting(r.Context(), a.db, githubKey(user), sealed)
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	a.mu.Lock()
	delete(a.devices, user)
	a.mu.Unlock()
	slog.Info("github account connected", "login", u.Login)
	writeJSON(w, map[string]string{"status": "connected", "login": u.Login})
}

// githubDisconnect forgets the token. Revoking it is done on GitHub
// (Settings → Applications), which needs no client secret.
func (a *remoteAPI) githubDisconnect(w http.ResponseWriter, r *http.Request) {
	if err := store.SetSetting(r.Context(), a.db, githubKey(requestUser(r.Context())), ""); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *remoteAPI) githubRepos(w http.ResponseWriter, r *http.Request) {
	token, err := a.Token(r.Context(), requestUser(r.Context()))
	if err != nil || a.github == nil {
		httpError(w, errors.New("connect a GitHub account in Settings first"), http.StatusBadRequest)
		return
	}
	rs, err := a.github.Repos(r.Context(), token)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, rs)
}

var repoName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)

func (a *remoteAPI) githubCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string `json:"name"`
		Private bool   `json:"private"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&in); err != nil || !repoName.MatchString(in.Name) {
		httpError(w, errors.New("repository names use letters, digits, '.', '-' and '_'"), http.StatusBadRequest)
		return
	}
	token, err := a.Token(r.Context(), requestUser(r.Context()))
	if err != nil || a.github == nil {
		httpError(w, errors.New("connect a GitHub account in Settings first"), http.StatusBadRequest)
		return
	}
	repo, err := a.github.CreateRepo(r.Context(), token, in.Name, in.Private)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, repo)
}
