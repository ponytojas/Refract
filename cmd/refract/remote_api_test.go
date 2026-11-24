package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/github"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

// Auto-push acts as the user who committed: it pushes only while they own
// the project.
func TestAutoPushRunsAsTheCommitter(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits, Key: make([]byte, 32)}
	ana := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	ben := auth.WithUser(context.Background(), auth.User{ID: "ben"})
	p, err := svc.Create(ana, "Paper")
	if err != nil {
		t.Fatal(err)
	}
	// Nothing listens there: a push fails fast, but it is attempted and recorded.
	if err := svc.SetRemote(ana, p.ID, projects.Remote{URL: "https://127.0.0.1:1/paper.git", Branch: "main", AutoPush: true}, false); err != nil {
		t.Fatal(err)
	}
	a := &remoteAPI{svc: svc, db: db, acc: &access.Checker{DB: db}}

	for name, ctx := range map[string]context.Context{"another user": ben, "no user": context.Background()} {
		a.AutoPush(ctx, p.ID)
		if _, pushed := a.last[p.ID]; pushed {
			t.Fatalf("%s: auto-push ran", name)
		}
	}
	a.AutoPush(ana, p.ID)
	if st, pushed := a.last[p.ID]; !pushed || st.Op != "push" || !st.Auto {
		t.Fatalf("owner: %+v, %v", st, pushed)
	}
}

// The owner can't keep a token across hosts by naming the new destination as
// its origin, and the refusal has a kind the web app translates.
func TestRemotePutIgnoresTokenOrigin(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	svc := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits, Key: make([]byte, 32)}
	ana := auth.WithUser(context.Background(), auth.User{ID: "ana"})
	p, err := svc.Create(ana, "Paper")
	if err != nil {
		t.Fatal(err)
	}
	a := &remoteAPI{svc: svc, db: db, acc: &access.Checker{DB: db}}
	put := func(body string) (int, map[string]any) {
		r := httptest.NewRequestWithContext(ana, "PUT", "/api/projects/"+p.ID+"/remote", strings.NewReader(body))
		r.SetPathValue("id", p.ID)
		w := httptest.NewRecorder()
		a.put(w, r)
		var out map[string]any
		json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if code, out := put(`{"url":"https://git.lab.example/a.git","token":"synthetic.tok"}`); code != 200 || out["has_token"] != true {
		t.Fatalf("save = %d %v", code, out)
	}
	code, out := put(`{"url":"https://evil.example/a.git","token_origin":"https://evil.example:443"}`)
	if e, _ := out["error"].(map[string]any); code != http.StatusBadRequest || e["kind"] != "remote_token_origin" {
		t.Errorf("forged origin = %d %v", code, out)
	}
	if r, _ := svc.Remote(ana, p.ID); r.URL != "https://git.lab.example/a.git" || r.Token != "synthetic.tok" {
		t.Errorf("remote after the refusal = %+v", r)
	}
	code, out = put(`{"url":"https://evil.example/a.git","github":true}`)
	if e, _ := out["error"].(map[string]any); code != http.StatusBadRequest || e["kind"] != "remote_github_origin" {
		t.Errorf("GitHub mode elsewhere = %d %v", code, out)
	}
	if st := a.record(p.ID, "push", true, projects.ErrTokenOrigin); st.ErrorKind != "remote_token_origin" {
		t.Errorf("sync status = %+v", st)
	}
	if st := a.record(p.ID, "push", true, errors.New("other")); st.ErrorKind != "" {
		t.Errorf("sync status = %+v", st)
	}
}

// On this computer the server opens GitHub's device page itself, and only
// GitHub's own page; elsewhere it leaves that to the browser.
func TestGitHubStartOpensTheDevicePage(t *testing.T) {
	verification := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dc", "user_code": "ABCD-1234", "verification_uri": verification, "expires_in": 900, "interval": 5})
	}))
	defer srv.Close()
	start := func(open func(string) error) (opened bool) {
		t.Helper()
		a := &remoteAPI{github: &github.Client{HTTP: srv.Client(), ClientID: "cid", Web: srv.URL, API: srv.URL}, openBrowser: open}
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/api/github/device", nil)
		a.githubStart(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "ana"})))
		var out struct {
			Opened bool `json:"opened"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != http.StatusOK {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		return out.Opened
	}
	var got []string
	open := func(u string) error { got = append(got, u); return nil }

	verification = srv.URL + "/login/device"
	if !start(open) || len(got) != 1 || got[0] != verification {
		t.Fatalf("opened %v", got)
	}
	verification = "file:///etc/passwd"
	if start(open) || len(got) != 1 {
		t.Fatalf("opened a page that isn't GitHub's: %v", got)
	}
	verification = srv.URL + "/login/device"
	if start(nil) {
		t.Fatal("reported opened without an opener (a server elsewhere)")
	}
	if start(func(string) error { return errors.New("no browser") }) {
		t.Fatal("reported opened when the browser failed")
	}
}
