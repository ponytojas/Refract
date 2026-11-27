package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/providers"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/tools"
)

const accountExportEndpoint = "/api/account/export"

func startAccountExport(t *testing.T, b *browser) string {
	t.Helper()
	res := b.post(accountExportEndpoint, nil)
	b.expect(res, http.StatusAccepted, "")
	var job struct{ ID string }
	if err := json.Unmarshal([]byte(res.Body), &job); err != nil || job.ID == "" {
		t.Fatalf("start export response: %s (%v)", res.Body, err)
	}
	return accountExportEndpoint + "/" + job.ID
}

func waitForAccountExport(t *testing.T, b *browser, endpoint string) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); ; {
		res := b.do("GET", endpoint, nil)
		if res.Status == http.StatusOK && strings.Contains(res.Body, `"ready"`) {
			return
		}
		if res.Status != http.StatusOK || strings.Contains(res.Body, `"failed"`) || time.Now().After(deadline) {
			t.Fatalf("export status: %d %s", res.Status, res.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readAccountZip(t *testing.T, raw []byte, forbidden ...string) map[string][]byte {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string][]byte{}
	for _, f := range archive.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range forbidden {
			if bytes.Contains(content, []byte(secret)) {
				t.Errorf("%s contains a credential or data from another account", f.Name)
			}
		}
		entries[f.Name] = content
	}
	return entries
}

func TestAccountExport(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	ctx := context.Background()
	const password = "fixture-owner-password"
	ana, err := e.people.SetPassword(ctx, "ana@example.com", password)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := e.people.SetPassword(ctx, "bob@example.com", "fixture-other-password")
	if err != nil {
		t.Fatal(err)
	}
	owner := auth.WithUser(ctx, ana)
	if err := e.people.SetLang(ctx, ana.ID, "es"); err != nil {
		t.Fatal(err)
	}
	create := func(ctx context.Context, name string) *projects.Project {
		t.Helper()
		p, err := e.ps.Create(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	draft := create(owner, "Active draft")
	trash := create(owner, "Deleted draft")
	foreign := create(auth.WithUser(ctx, bob), "Another user's draft")
	if err := e.ps.WriteFile(owner, draft.ID, "main.tex", strings.NewReader("Committed text")); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.ps.Checkpoint(owner, draft.ID, "second commit"); err != nil || !ok {
		t.Fatalf("checkpoint: %t %v", ok, err)
	}
	if err := e.ps.WriteFile(owner, draft.ID, "main.tex", strings.NewReader("Latest uncommitted edit")); err != nil {
		t.Fatal(err)
	}
	template, err := e.ps.SaveTemplate(owner, draft.ID, "Private starter")
	if err != nil {
		t.Fatal(err)
	}
	query := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.people.DB.ExecContext(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []*projects.Project{draft, trash, foreign} {
		query(`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES (?, ?, ?, 0, 0)`, p.ID, p.ID, p.Name+" conversation")
		query(`INSERT INTO turns (id, chat_id, message, reply, status, error, created_at, updated_at)
			VALUES (?, ?, ?, ?, 'failed', ?, 0, 0)`, p.ID, p.ID, "Prompt in "+p.Name, "Reply in "+p.Name,
			`{"message":"fixture-raw-provider-error-secret"}`)
	}
	if err := e.ps.Trash(owner, trash.ID); err != nil {
		t.Fatal(err)
	}
	const apiKey = "fixture-provider-key"
	const endpointSecret = "fixture-endpoint-secret"
	const gitSecret = "fixture-git-config-secret"
	providerStore := &providers.Store{DB: e.people.DB, Key: e.people.Key}
	err = providerStore.Save(ctx, ana.ID, providers.Config{
		ID: "compat", Kind: "compat", Name: "My provider", APIKey: apiKey, BaseURL: "https://example.com/v1?token=" + endpointSecret,
	})
	if err != nil {
		t.Fatal(err)
	}
	setting := store.UserKey(ana.ID, "providers")
	sealed, err := store.GetSetting(ctx, e.people.DB, setting)
	if err != nil || sealed == "" {
		t.Fatalf("stored provider settings: %v", err)
	}
	var passwordHash string
	if err := e.people.DB.QueryRowContext(ctx, `SELECT password FROM users WHERE id = ?`, ana.ID).Scan(&passwordHash); err != nil {
		t.Fatal(err)
	}
	forbidden := []string{password, passwordHash, apiKey, endpointSecret, gitSecret, sealed,
		"fixture-raw-provider-error-secret", foreign.Name, foreign.ID}
	for key, value := range map[string]string{
		store.UserKey(ana.ID, "siwc.credentials"): "fixture-chatgpt-credentials",
		store.UserKey(ana.ID, "github"):           "fixture-github-token",
		"remote:" + draft.ID:                      "fixture-git-remote-token",
	} {
		ciphertext, err := store.Seal(e.people.Key, []byte(value), key)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetSetting(ctx, e.people.DB, key, ciphertext); err != nil {
			t.Fatal(err)
		}
		forbidden = append(forbidden, value, ciphertext)
	}
	git, err := tools.Path("git")
	if err != nil {
		t.Fatal(err)
	}
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := exec.Command(git, args...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	gitRun("-C", e.ps.Path(draft.ID), "config", "remote.fixture.url", "https://example.com/"+gitSecret)

	login := func(email, password string) *browser {
		b := e.browser()
		b.expect(b.post("/api/auth/login", map[string]string{"email": email, "password": password}), http.StatusNoContent, "")
		return b
	}
	a := login(ana.Email, password)
	b := login(bob.Email, "fixture-other-password")
	var sessionHash string
	if err := e.people.DB.QueryRowContext(ctx, `SELECT id FROM sessions WHERE user_id = ?`, ana.ID).Scan(&sessionHash); err != nil {
		t.Fatal(err)
	}
	forbidden = append(forbidden, sessionHash)
	old := startAccountExport(t, a)
	waitForAccountExport(t, a, old)
	if err := e.ps.WriteFile(owner, draft.ID, "main.tex", strings.NewReader("Edit after the first export")); err != nil {
		t.Fatal(err)
	}
	endpoint := startAccountExport(t, a)
	waitForAccountExport(t, a, endpoint)
	a.expect(a.do("GET", old+"/download", nil), http.StatusNotFound, "")
	for _, path := range []string{endpoint, endpoint + "/download"} {
		b.expect(b.do("GET", path, nil), http.StatusNotFound, "")
	}
	result := a.do("GET", endpoint+"/download", nil)
	if result.Status != http.StatusOK {
		t.Fatalf("download: %d %s", result.Status, result.Body)
	}
	entries := readAccountZip(t, []byte(result.Body), forbidden...)
	var manifest []projects.ExportProject
	if err := json.Unmarshal(entries["projects.json"], &manifest); err != nil || len(manifest) != 3 {
		t.Fatalf("project manifest: %s (%v)", entries["projects.json"], err)
	}
	for _, p := range manifest {
		if (p.DeletedAt != nil) != (p.ID == trash.ID) || p.IsTemplate != (p.ID == template.ID) {
			t.Errorf("unexpected metadata: %+v", p)
		}
		prefix := fmt.Sprintf("projects/%s/", p.ID)
		if len(entries[prefix+"history.bundle"]) == 0 || !json.Valid(entries[prefix+"chats.json"]) {
			t.Errorf("%s is missing history or chats", p.Name)
		}
		if p.ID != template.ID {
			for _, s := range []string{"Prompt in " + p.Name, "Reply in " + p.Name} {
				if !bytes.Contains(entries[prefix+"chats.json"], []byte(s)) {
					t.Errorf("%s lacks %s", prefix+"chats.json", s)
				}
			}
		}
	}
	if settings := string(entries["settings.json"]); !strings.Contains(settings, `"email_language":"es"`) ||
		!strings.Contains(settings, "My provider") || strings.Contains(settings, "base_url") {
		t.Errorf("unexpected settings: %s", settings)
	}
	prefix := "projects/" + draft.ID + "/"
	if text := string(entries[prefix+"files/main.tex"]); text != "Edit after the first export" {
		t.Errorf("worktree file = %q", text)
	}
	dir := t.TempDir()
	bundle := filepath.Join(dir, "repo.bundle")
	if err := os.WriteFile(bundle, entries[prefix+"history.bundle"], 0o600); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(dir, "clone")
	gitRun("clone", bundle, clone)
	if n := strings.TrimSpace(gitRun("-C", clone, "rev-list", "--count", "HEAD")); n != "2" {
		t.Errorf("bundle has %s commits, want 2", n)
	}
	if text := gitRun("-C", clone, "show", "HEAD:main.tex"); text != "Committed text" {
		t.Errorf("bundle HEAD = %q", text)
	}
}

func TestAccountExportSkipsMissingProject(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	ana, err := e.people.SetPassword(t.Context(), "ana@example.com", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	ctx := auth.WithUser(t.Context(), ana)
	first, err := e.ps.Create(ctx, "Available project")
	if err != nil {
		t.Fatal(err)
	}
	missing, err := e.ps.Create(ctx, "Missing project")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(e.ps.Path(missing.ID), filepath.Join(t.TempDir(), "moved-repo")); err != nil {
		t.Fatal(err)
	}
	b := e.browser()
	b.expect(b.post("/api/auth/login", map[string]string{"email": ana.Email, "password": "fixture-password"}), http.StatusNoContent, "")
	endpoint := startAccountExport(t, b)
	waitForAccountExport(t, b, endpoint)
	res := b.do("GET", endpoint+"/download", nil)
	b.expect(res, http.StatusOK, "")
	entries := readAccountZip(t, []byte(res.Body))
	var omitted []struct {
		ID string `json:"project_id"`
	}
	if err := json.Unmarshal(entries["errors.json"], &omitted); err != nil || len(omitted) != 1 || omitted[0].ID != missing.ID {
		t.Fatalf("omitted projects: %s (%v)", entries["errors.json"], err)
	}
	if len(entries["projects/"+first.ID+"/history.bundle"]) == 0 {
		t.Error("archive does not contain the available project's history")
	}
	for entry := range entries {
		if strings.HasPrefix(entry, "projects/"+missing.ID+"/") {
			t.Errorf("partial project entry: %s", entry)
		}
	}
}

func TestAccountExportReplacesPreparingJobAtCapacity(t *testing.T) {
	e := newAccountEnv(t, envOpts{noMail: true})
	ana, err := e.people.SetPassword(t.Context(), "ana@example.com", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := e.people.SetPassword(t.Context(), "bob@example.com", "fixture-password")
	if err != nil {
		t.Fatal(err)
	}
	b := e.browser()
	b.expect(b.post("/api/auth/login", map[string]string{"email": ana.Email, "password": "fixture-password"}), http.StatusNoContent, "")
	e.people.DB.SetMaxOpenConns(1)
	conn, err := e.people.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close() // keep both workers waiting for the database
	exports := e.login.exports
	old := exports.start(ana.ID)
	other := exports.start(bob.ID)
	replacement := exports.start(ana.ID)
	if old == "" || other == "" || replacement == "" || replacement == old || exports.start("third") != "" {
		t.Fatalf("unexpected job IDs %q, %q, %q or third export accepted", old, other, replacement)
	}
	conn.Close()
	waitForAccountExport(t, b, accountExportEndpoint+"/"+replacement)
	b.expect(b.do("GET", accountExportEndpoint+"/"+old+"/download", nil), http.StatusNotFound, "")
	exports.close()
	if entries, err := os.ReadDir(exports.tmp); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
		t.Errorf("staging files remain after close: %v (%v)", entries, err)
	}
}

func TestAccountExportStartupCleansStagingFiles(t *testing.T) {
	tmp := t.TempDir()
	staging := filepath.Join(tmp, "account-exports")
	stale := filepath.Join(staging, exportDirPrefix+"abandoned")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(staging, "unrelated")
	if err := os.WriteFile(other, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	tr := newAccountExports(t.Context(), nil, tmp)
	defer tr.close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("abandoned staging directory was not removed: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("unrelated file was removed: %v", err)
	}
}

type blockedExportWriter struct {
	http.ResponseWriter
	started chan struct{}
	release chan struct{}
}

func (w blockedExportWriter) Write(p []byte) (int, error) {
	close(w.started)
	<-w.release
	return w.ResponseWriter.Write(p)
}

func TestAccountExportDefersCleanupUntilDownloadCloses(t *testing.T) {
	exports := newAccountExports(t.Context(), nil, t.TempDir())
	defer exports.close()
	dir := filepath.Join(exports.tmp, exportDirPrefix+"download")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "account.zip"), []byte("archive data"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	exports.jobs["owner"] = &accountExport{id: "download", status: "ready", dir: dir, ctx: ctx, cancel: cancel}
	r := httptest.NewRequest(http.MethodGet, "/api/account/export/download/download", nil)
	r.SetPathValue("id", "download")
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "owner"}))
	rec := httptest.NewRecorder()
	w := blockedExportWriter{rec, make(chan struct{}), make(chan struct{})}
	done := make(chan struct{})
	go func() {
		defer close(done)
		exports.downloadExport(w, r)
	}()
	select {
	case <-w.started:
	case <-done:
		t.Fatalf("download finished early with status %d", rec.Code)
	case <-time.After(5 * time.Second):
		close(w.release)
		t.Fatal("download did not start")
	}
	exports.discard("owner")
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("staging directory removed during download: %v", err)
	}
	close(w.release)
	<-done
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("staging directory not removed after download: %v", err)
	}
	if rec.Body.String() != "archive data" {
		t.Errorf("download body = %q", rec.Body.String())
	}
}

func TestUsersExport(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("REFRACT_DATA_DIR", dir)
	t.Setenv("REFRACT_SECRET_KEY", base64.StdEncoding.EncodeToString(make([]byte, 32)))
	t.Setenv("REFRACT_SECRET_KEY_FILE", "")
	if err := runUsers([]string{"add", "ana@example.com"}, io.Discard); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ana.zip")
	args := []string{"export", "ANA@example.com", path}
	if err := runUsers(args, io.Discard); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	entries := readAccountZip(t, raw)
	if !bytes.Contains(entries["settings.json"], []byte("ana@example.com")) {
		t.Fatal("CLI archive is missing the selected account")
	}
	if err := runUsers(args, io.Discard); err == nil {
		t.Fatal("export replaced an existing archive")
	}
	again, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(raw, again) {
		t.Fatal("the existing archive changed")
	}
}
