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
	"strconv"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mode"
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

// table is the production route table, with every optional route included.
func table(acc *access.Checker) *router {
	rt := &router{mux: http.NewServeMux(), acc: acc}
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
	const reached = 299
	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(reached) })
	as := func(user string) context.Context {
		return auth.WithUser(context.Background(), auth.User{ID: user})
	}
	for _, r := range rt.routes {
		h := rt.guard(r, sentinel)
		mux := http.NewServeMux() // so path values are set, as in production
		mux.Handle(r.Pattern, h)
		call := func(ctx context.Context, o owned) int {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, request(r, o).WithContext(ctx))
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

// No route reaches the signed-in app without the table: outside it, only
// the server's own plumbing in run and the sign-in routes register handlers.
func TestNoRoutesOutsideTheTable(t *testing.T) {
	allowed := map[string][]string{
		"register": nil, // the table itself
		"handler":  nil, // loginAPI: /api/auth/*, before anyone is signed in
		"run":      {`"/api/"`, `"/"`, `"GET /healthz"`, `"/api/auth/"`},
	}
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
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
					return true
				}
				patterns, ok := allowed[fn.Name.Name]
				pattern := ""
				if lit, isLit := call.Args[0].(*ast.BasicLit); isLit {
					pattern = lit.Value
				}
				if ok && (patterns == nil || contains(patterns, pattern)) {
					return true
				}
				p, _ := strconv.Unquote(pattern)
				t.Errorf("%s: %s registers %q outside the route table (use router.user/project/chat/turn)", fset.Position(call.Pos()), fn.Name.Name, p)
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
