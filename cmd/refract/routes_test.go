package main

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/assistant"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

// ids of what each user owns in the walk's database.
type owned struct{ project, chat, turn string }

var (
	ana = owned{"p-ana", "c-ana", "t-ana"}
	ben = owned{"p-ben", "c-ben", "t-ben"}
)

func accessDB(t *testing.T) *access.Checker {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	for user, o := range map[string]owned{"ana": ana, "ben": ben} {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO projects (id, name, created_at, updated_at, owner) VALUES (?, 'P', 0, 0, ?)`, []any{o.project, user}},
			{`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES (?, ?, 'C', 0, 0)`, []any{o.chat, o.project}},
			{`INSERT INTO turns (id, chat_id, message, status, created_at, updated_at) VALUES (?, ?, 'M', 'review', 0, 0)`, []any{o.turn, o.chat}},
		} {
			if _, err := db.Exec(q.sql, q.args...); err != nil {
				t.Fatal(err)
			}
		}
	}
	return &access.Checker{DB: db}
}

// reached is what a handler in the walked table answers.
const reached = 299

// table is the production route table, with every optional route included and
// each handler replaced by one answering reached. Registration and the guard
// are the real ones.
func table(acc *access.Checker) *router {
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(reached) })
	rt := &router{mux: http.NewServeMux(), acc: acc, stub: func(route) http.HandlerFunc { return sentinel }}
	registerRoutes(rt, &api{mode: mode.SelfHost}, &projectsAPI{}, &compileAPI{}, &assistantAPI{}, &remoteAPI{}, &papersAPI{}, &loginAPI{})
	return rt
}

// request fills a route's wildcards with o's ids.
func request(r route, o owned) *http.Request {
	method, path, _ := strings.Cut(r.Pattern, " ")
	path = strings.NewReplacer("{chat}", o.chat, "{turn}", o.turn, "{hash}", "abc").Replace(path)
	if r.Scope == scopeProject {
		path = strings.Replace(path, "{id}", o.project, 1)
	}
	path = strings.ReplaceAll(path, "{id}", "x") // other ids, e.g. a provider's
	return httptest.NewRequest(method, path, nil)
}

// Every route in the table, called as its owner, a stranger and nobody: only
// the owner gets through to anything that addresses a project, chat or turn.
func TestRouteTableAccess(t *testing.T) {
	rt := table(accessDB(t))
	if len(rt.routes) < 40 {
		t.Fatalf("only %d routes in the table", len(rt.routes))
	}
	as := func(user string) context.Context {
		return auth.WithUser(context.Background(), auth.User{ID: user})
	}
	for _, r := range rt.routes {
		call := func(ctx context.Context, o owned) int {
			w := httptest.NewRecorder()
			rt.mux.ServeHTTP(w, request(r, o).WithContext(ctx))
			return w.Code
		}
		scoped := r.Scope != scopeUser
		for _, tc := range []struct {
			who      string
			ctx      context.Context
			ids      owned
			want     int
			wantUser int
		}{
			{"owner", as("ana"), ana, reached, reached},
			{"stranger", as("ben"), ana, http.StatusNotFound, reached},
			{"owner of other ids", as("ana"), ben, http.StatusNotFound, reached},
			{"empty user id", as(""), ana, http.StatusNotFound, http.StatusForbidden},
			{"anonymous", context.Background(), ana, http.StatusNotFound, http.StatusForbidden},
		} {
			want := tc.wantUser
			if scoped {
				want = tc.want
			}
			if got := call(tc.ctx, tc.ids); got != want {
				t.Errorf("%s as %s: %d, want %d", r.Pattern, tc.who, got, want)
			}
		}
	}
}

func TestRouteScopeIsValidated(t *testing.T) {
	for _, r := range []route{
		{Pattern: "GET /api/projects/{id}/files", Scope: scopeUser},
		{Pattern: "GET /api/chats/{chat}/turns", Scope: scopeProject, Min: access.Owner},
		{Pattern: "GET /api/projects/{id}/files", Scope: scopeProject}, // no role
		{Pattern: "POST /api/turns/{turn}/accept", Scope: scopeUser},
		{Pattern: "/api/projects/{id}", Scope: scopeProject, Min: access.Owner}, // no method
		// Review of #20: a project under another wildcard name, as a user route.
		{Pattern: "GET /api/projects/{project}/review-leak", Scope: scopeUser},
		{Pattern: "GET /api/projects/{project}/files", Scope: scopeProject, Min: access.Owner},
		{Pattern: "GET /api/projects/{id...}", Scope: scopeUser},
		{Pattern: "GET /api/chats/{c}/turns", Scope: scopeUser},
		{Pattern: "GET /api/turns/latest", Scope: scopeUser},
		{Pattern: "GET /api/projects/import/{x}", Scope: scopeUser},
		{Pattern: "GET /api/documents/{project}", Scope: scopeUser}, // any wildcard in a user route
	} {
		if r.validate() == nil {
			t.Errorf("%+v: accepted", r)
		}
	}
	for _, r := range []route{
		{Pattern: "GET /api/projects", Scope: scopeUser},
		{Pattern: "POST /api/projects/import", Scope: scopeUser},
		{Pattern: "GET /api/projects/{id}", Scope: scopeProject, Min: access.Owner},
		{Pattern: "PUT /api/providers/{id}", Scope: scopeUser},
	} {
		if err := r.validate(); err != nil {
			t.Errorf("%+v: %v", r, err)
		}
	}
}

// No route reaches the signed-in app without the table. Outside it, only
// these exact registrations exist: the server's plumbing in run, and the
// sign-in routes, served before anyone is signed in. Adding one here is a
// decision to review, not a formality.
var allowedRegistrations = map[string][]string{
	"routes.go:register": {"r.Pattern"}, // the table itself
	"main.go:run":        {`"/api/"`, `"/"`, `"GET /healthz"`, `"/api/auth/"`},
	"login_api.go:handler": {
		`"GET /api/auth/config"`, `"POST /api/auth/login"`, `"POST /api/auth/logout"`,
		`"GET /api/auth/oidc/start"`, `"GET /api/auth/oidc/callback"`,
		`"/api/auth/oidc/"`, `"/api/auth/"`,
	},
}

func TestNoRoutesOutsideTheTable(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			where := name + ":" + fn.Name.Name
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") || len(call.Args) == 0 {
					return true
				}
				arg := string(src[fset.Position(call.Args[0].Pos()).Offset:fset.Position(call.Args[0].End()).Offset])
				if !contains(allowedRegistrations[where], arg) {
					t.Errorf("%s: %s registers %s outside the route table (use router.user/project/chat/turn)", fset.Position(call.Pos()), where, arg)
				}
				return true
			})
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// The real handlers behind the table: another user's chat, turn and project
// ids are not found, and nothing they ask for changes anything.
func TestHandlersKeepUsersApart(t *testing.T) {
	acc := accessDB(t)
	ps := &projects.Service{DB: acc.DB, Dir: t.TempDir()}
	rt := &router{mux: http.NewServeMux(), acc: acc}
	(&projectsAPI{svc: ps}).routes(rt)
	(&assistantAPI{svc: &assistant.Service{DB: acc.DB, Projects: ps}}).routes(rt)
	call := func(user, method, path string) int {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, nil)
		rt.mux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: user})))
		return w.Code
	}
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/projects/" + ana.project},
		{"GET", "/api/projects/" + ana.project + "/chats"},
		{"POST", "/api/projects/" + ana.project + "/chats"},
		{"GET", "/api/chats/" + ana.chat + "/turns"},
		{"POST", "/api/turns/" + ana.turn + "/reject"},
		{"DELETE", "/api/projects/" + ana.project},
	} {
		if got := call("ben", tc.method, tc.path); got != http.StatusNotFound {
			t.Errorf("ben: %s %s = %d, want 404", tc.method, tc.path, got)
		}
	}
	var status string
	acc.DB.QueryRow(`SELECT status FROM turns WHERE id = ?`, ana.turn).Scan(&status)
	var chats int
	acc.DB.QueryRow(`SELECT count(*) FROM chats WHERE project_id = ?`, ana.project).Scan(&chats)
	if status != "review" || chats != 1 {
		t.Errorf("ben changed ana's data: turn %q, %d chats", status, chats)
	}
	for _, path := range []string{"/api/projects/" + ana.project + "/chats", "/api/chats/" + ana.chat + "/turns"} {
		if got := call("ana", "GET", path); got != http.StatusOK {
			t.Errorf("ana: GET %s = %d", path, got)
		}
	}
	if ps, _ := ps.List(auth.WithUser(context.Background(), auth.User{ID: "ben"})); len(ps) != 1 || ps[0].ID != ben.project {
		t.Errorf("ben lists %+v", ps)
	}
}
