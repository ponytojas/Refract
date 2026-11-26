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
	const endpoint = "/api/account/export"
	b.expect(a.post(endpoint, nil), http.StatusAccepted, "")
	for deadline := time.Now().Add(10 * time.Second); ; {
		res := a.do("GET", endpoint, nil)
		if res.Status == http.StatusOK && strings.Contains(res.Body, `"ready"`) {
			break
		}
		if res.Status != http.StatusOK || strings.Contains(res.Body, `"failed"`) || time.Now().After(deadline) {
			t.Fatalf("export status: %d %s", res.Status, res.Body)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	if text := string(entries[prefix+"files/main.tex"]); text != "Latest uncommitted edit" {
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
