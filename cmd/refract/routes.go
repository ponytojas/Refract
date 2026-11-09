package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ponytojas/Refract/internal/access"
)

// The route table: every API route under the signed-in app is registered
// here with what it touches and the role it needs, and the wrapper checks
// that role before the handler runs. Handlers and services trust it, so a
// route can't forget the check: the scope is validated against the pattern
// at startup, and routes_test.go walks the whole table.

type scope int

const (
	scopeUser    scope = iota // the user's own things: no project in the path
	scopeProject              // /api/projects/{id}…
	scopeChat                 // /api/chats/{chat}…
	scopeTurn                 // /api/turns/{turn}…
)

var scopePrefix = map[scope]string{
	scopeProject: "/api/projects/{id}",
	scopeChat:    "/api/chats/{chat}",
	scopeTurn:    "/api/turns/{turn}",
}

type route struct {
	Pattern string // "METHOD /path"
	Scope   scope
	Min     access.Role
}

type router struct {
	mux    *http.ServeMux
	acc    *access.Checker
	routes []route
	// stub, in tests, replaces each handler before it is registered, so the
	// table can be walked through the real registration and guard.
	stub func(route) http.HandlerFunc
}

func (rt *router) user(pattern string, h http.HandlerFunc) {
	rt.register(route{Pattern: pattern, Scope: scopeUser}, h)
}

func (rt *router) project(pattern string, min access.Role, h http.HandlerFunc) {
	rt.register(route{Pattern: pattern, Scope: scopeProject, Min: min}, h)
}

func (rt *router) chat(pattern string, min access.Role, h http.HandlerFunc) {
	rt.register(route{Pattern: pattern, Scope: scopeChat, Min: min}, h)
}

func (rt *router) turn(pattern string, min access.Role, h http.HandlerFunc) {
	rt.register(route{Pattern: pattern, Scope: scopeTurn, Min: min}, h)
}

func (rt *router) register(r route, h http.HandlerFunc) {
	if err := r.validate(); err != nil {
		panic(err) // a programming error: fail at startup, not on a request
	}
	rt.routes = append(rt.routes, r)
	if rt.stub != nil {
		h = rt.stub(r)
	}
	rt.mux.Handle(r.Pattern, rt.guard(r, h))
}

// validate ties the scope to the path: anything addressing a project, chat or
// turn must be registered with that scope and a role.
func (r route) validate() error {
	_, path, ok := strings.Cut(r.Pattern, " ")
	if !ok {
		return fmt.Errorf("route %q: want \"METHOD /path\"", r.Pattern)
	}
	under := func(prefix string) bool { return path == prefix || strings.HasPrefix(path, prefix+"/") }
	for s, prefix := range scopePrefix {
		if under(prefix) && r.Scope != s {
			return fmt.Errorf("route %q addresses %s and must be registered with that scope", r.Pattern, prefix)
		}
	}
	if r.Scope != scopeUser {
		if !under(scopePrefix[r.Scope]) {
			return fmt.Errorf("route %q is not under %s", r.Pattern, scopePrefix[r.Scope])
		}
		if r.Min == access.None {
			return fmt.Errorf("route %q needs a minimum role", r.Pattern)
		}
	}
	return nil
}

// guard runs h only when the request's user has the route's role on the
// project the path addresses.
func (rt *router) guard(r route, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ctx := req.Context()
		if _, err := access.UserFrom(ctx); err != nil {
			if r.Scope == scopeUser {
				httpError(w, err, http.StatusForbidden)
			} else {
				accessError(w, access.ErrNotFound)
			}
			return
		}
		var project string
		var err error
		switch r.Scope {
		case scopeUser:
			h.ServeHTTP(w, req)
			return
		case scopeProject:
			project = req.PathValue("id")
		case scopeChat:
			project, err = rt.acc.ChatProject(ctx, req.PathValue("chat"))
		case scopeTurn:
			project, err = rt.acc.TurnProject(ctx, req.PathValue("turn"))
		}
		if err == nil {
			err = rt.acc.Require(ctx, project, r.Min)
		}
		if err != nil {
			accessError(w, err)
			return
		}
		h.ServeHTTP(w, req)
	})
}

func accessError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, access.ErrNotFound):
		httpError(w, errors.New("not found"), http.StatusNotFound)
	case errors.Is(err, access.ErrForbidden):
		httpError(w, errors.New("not allowed"), http.StatusForbidden)
	default:
		slog.Error("access", "err", err)
		httpError(w, errors.New("internal error"), http.StatusInternalServerError)
	}
}

// registerRoutes is the whole table, used by run and by the tests. login is
// nil unless Refract signs people in itself.
func registerRoutes(rt *router, a *api, pa *projectsAPI, ca *compileAPI, asa *assistantAPI, ra *remoteAPI, pp *papersAPI, login *loginAPI) {
	a.routes(rt)
	pa.routes(rt)
	ca.routes(rt)
	asa.routes(rt)
	ra.routes(rt)
	pp.routes(rt)
	if login != nil {
		login.accountRoutes(rt)
	}
}
