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
	"slices"
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
// trashed, trashedChat and trashedTurn are the user's project in the trash,
// with a chat and a turn.
type owned struct{ project, chat, turn, trashed, trashedChat, trashedTurn string }

var (
	ana = owned{"p-ana", "c-ana", "t-ana", "p-ana-trashed", "c-ana-trashed", "t-ana-trashed"}
	ben = owned{"p-ben", "c-ben", "t-ben", "p-ben-trashed", "c-ben-trashed", "t-ben-trashed"}
)

func accessDB(t *testing.T) *access.Checker {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	// ana and ben have verified emails, una has not.
	for id, verified := range map[string]int{"ana": 1, "ben": 1, "una": 0} {
		if _, err := db.Exec(`INSERT INTO users (id, email, created_at, email_verified_at) VALUES (?, ?, 1, ?)`, id, id+"@example.com", verified); err != nil {
			t.Fatal(err)
		}
	}
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
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`INSERT INTO projects (id, name, created_at, updated_at, owner, deleted_at) VALUES (?, 'T', 0, 0, ?, 1)`, []any{o.trashed, user}},
			{`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES (?, ?, 'C', 0, 0)`, []any{o.trashedChat, o.trashed}},
			{`INSERT INTO turns (id, chat_id, message, status, created_at, updated_at) VALUES (?, ?, 'M', 'review', 0, 0)`, []any{o.trashedTurn, o.trashedChat}},
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
	switch r.Scope {
	case scopeProject:
		path = strings.Replace(path, "{id}", o.project, 1)
	case scopeTrash:
		path = strings.Replace(path, "{id}", o.trashed, 1)
	}
	path = strings.ReplaceAll(path, "{id}", "x") // other ids, e.g. a provider's
	return httptest.NewRequest(method, path, nil)
}

// Every route in the table, called as its owner, a stranger and nobody: only
// the owner gets through to anything that addresses a project, chat or turn.
func TestRouteTableAccess(t *testing.T) {
	rt := table(accessDB(t))
	if len(rt.routes) < 43 {
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

// A missing resource and another user's resource must be indistinguishable.
func TestMissingResourcesMatchInaccessibleResources(t *testing.T) {
	rt := table(accessDB(t))
	ctx := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	missing := owned{"missing-project", "missing-chat", "missing-turn", "missing-trashed", "missing-tc", "missing-tt"}

	for _, r := range rt.routes {
		if r.Scope == scopeUser {
			continue
		}
		t.Run(r.Pattern, func(t *testing.T) {
			call := func(ids owned) *httptest.ResponseRecorder {
				w := httptest.NewRecorder()
				rt.mux.ServeHTTP(w, request(r, ids).WithContext(ctx))
				return w
			}
			inaccessible := call(ben)
			nonexistent := call(missing)
			for name, response := range map[string]*httptest.ResponseRecorder{
				"inaccessible": inaccessible,
				"nonexistent":  nonexistent,
			} {
				if response.Code != http.StatusNotFound {
					t.Fatalf("%s: status = %d, want 404", name, response.Code)
				}
			}
			if nonexistent.Body.String() != inaccessible.Body.String() {
				t.Errorf("404 bodies differ: nonexistent = %q, inaccessible = %q",
					nonexistent.Body.String(), inaccessible.Body.String())
			}
		})
	}
}

// Every route that addresses a project, chat or turn answers 404 for what is
// in the trash, even to its owner; the trash routes answer 404 for a live
// project.
func TestTrashedProjectsAreInvisible(t *testing.T) {
	rt := table(accessDB(t))
	ctx := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	trashed := owned{project: ana.trashed, chat: ana.trashedChat, turn: ana.trashedTurn}
	checked := 0
	for _, r := range rt.routes {
		var ids owned
		switch r.Scope {
		case scopeUser:
			continue
		case scopeTrash:
			ids = owned{trashed: ana.project} // the live project
		default:
			ids = trashed
		}
		w := httptest.NewRecorder()
		rt.mux.ServeHTTP(w, request(r, ids).WithContext(ctx))
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", r.Pattern, w.Code)
		}
		checked++
	}
	if checked < 30 {
		t.Errorf("only %d routes checked", checked)
	}
}

// The owner of a trashed project is told so on every project, chat and turn
// route; everyone else, and every other 404, gets the plain body.
func TestOwnerIsToldTheProjectIsTrashed(t *testing.T) {
	acc := accessDB(t)
	rt := table(acc)
	as := func(user string) context.Context { return auth.WithUser(context.Background(), auth.User{ID: user}) }
	trashed := owned{project: ana.trashed, chat: ana.trashedChat, turn: ana.trashedTurn}
	missing := owned{"missing-project", "missing-chat", "missing-turn", "missing-trashed", "missing-tc", "missing-tt"}
	call := func(r route, ctx context.Context, ids owned) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		rt.mux.ServeHTTP(w, request(r, ids).WithContext(ctx))
		return w
	}
	for _, r := range rt.routes {
		if r.Scope == scopeUser {
			continue
		}
		if r.Scope == scopeTrash {
			// The trash routes keep the plain 404 for a live project.
			if w := call(r, as("ana"), owned{trashed: ana.project}); w.Code != 404 || strings.Contains(w.Body.String(), "project_trashed") {
				t.Errorf("%s on a live project: %d %s", r.Pattern, w.Code, w.Body)
			}
			continue
		}
		w := call(r, as("ana"), trashed)
		if w.Code != 404 || !strings.Contains(w.Body.String(), `"kind":"project_trashed"`) {
			t.Errorf("%s as the owner: %d %s", r.Pattern, w.Code, w.Body)
		}
		stranger, unknown := call(r, as("ben"), trashed), call(r, as("ben"), missing)
		if stranger.Code != 404 || stranger.Body.String() != unknown.Body.String() {
			t.Errorf("%s as a stranger: %d %q, unknown id: %q", r.Pattern, stranger.Code, stranger.Body, unknown.Body)
		}
	}
	// After a purge the kind is "other" again.
	acc.DB.Exec(`DELETE FROM projects WHERE id = ?`, ana.trashed)
	for _, r := range rt.routes {
		if r.Scope != scopeProject {
			continue
		}
		if w := call(r, as("ana"), trashed); strings.Contains(w.Body.String(), "project_trashed") {
			t.Errorf("%s after a purge: %s", r.Pattern, w.Body)
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
		{Pattern: "GET /api/documents/{project}", Scope: scopeUser},                       // any wildcard in a user route
		{Pattern: "GET /api/projects/{id}/files", Scope: scopeTrash, Min: access.Owner},   // not in trashRoutes
		{Pattern: "POST /api/projects/{id}/restore", Scope: scopeTrash},                   // no role
		{Pattern: "POST /api/chats/{chat}/restore", Scope: scopeTrash, Min: access.Owner}, // not under a project
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
		{Pattern: "POST /api/projects/{id}/restore", Scope: scopeTrash, Min: access.Owner},
		{Pattern: "DELETE /api/projects/{id}/purge", Scope: scopeTrash, Min: access.Owner},
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
		// Served to people who aren't signed in yet: they ask for sign-up, a new password or to confirm an address.
		`"POST /api/auth/signup"`, `"POST /api/auth/forgot"`, `"POST /api/auth/reset"`, `"POST /api/auth/verify"`,
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
		{"POST", "/api/projects/" + ana.trashed + "/restore"},
		{"DELETE", "/api/projects/" + ana.trashed + "/purge"},
	} {
		if got := call("ben", tc.method, tc.path); got != http.StatusNotFound {
			t.Errorf("ben: %s %s = %d, want 404", tc.method, tc.path, got)
		}
	}
	var trashedAt int
	acc.DB.QueryRow(`SELECT deleted_at FROM projects WHERE id = ?`, ana.trashed).Scan(&trashedAt)
	if trashedAt != 1 {
		t.Errorf("ben changed ana's trashed project: deleted_at = %d", trashedAt)
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

// Creating or importing a project needs a verified email, declared on the route
// and enforced by the guard: no other route asks for it.
func TestRouteNeeds(t *testing.T) {
	rt := table(accessDB(t))
	want := []string{"POST /api/projects", "POST /api/projects/import", "POST /api/projects/import-folder"}
	var got []string
	for _, r := range rt.routes {
		if slices.Contains(r.Needs, access.VerifiedEmail) {
			got = append(got, r.Pattern)
		}
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("routes needing a verified email = %v, want %v", got, want)
	}
	as := func(user string) context.Context {
		return auth.WithUser(context.Background(), auth.User{ID: user})
	}
	for _, r := range rt.routes {
		if r.Scope != scopeUser {
			continue
		}
		needs := slices.Contains(want, r.Pattern)
		for _, who := range []string{"una", "nobody"} {
			w := httptest.NewRecorder()
			rt.mux.ServeHTTP(w, request(r, ana).WithContext(as(who)))
			switch {
			case !needs && w.Code != reached:
				t.Errorf("%s as %s = %d, want it reached", r.Pattern, who, w.Code)
			case needs && (w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), `"email_unverified"`)):
				t.Errorf("%s as %s = %d %s, want 403 email_unverified", r.Pattern, who, w.Code, w.Body.String())
			}
		}
		if needs {
			w := httptest.NewRecorder()
			rt.mux.ServeHTTP(w, request(r, ana).WithContext(as("ana")))
			if w.Code != reached {
				t.Errorf("%s as a verified user = %d", r.Pattern, w.Code)
			}
		}
	}
}
