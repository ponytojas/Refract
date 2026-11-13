package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/providers"
	"github.com/ponytojas/Refract/internal/siwc"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/usage"
)

// newAIServer serves the AI routes for one mode, for user "ana", who has a
// ChatGPT credential with plan usage stored from before.
func newAIServer(t *testing.T, m mode.Mode) http.Handler {
	t.Helper()
	h, _ := newAIServerDB(t, m)
	return h
}

// newAIServerDB is newAIServer that also returns its database.
func newAIServerDB(t *testing.T, m mode.Mode) (http.Handler, *sql.DB) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	key := make([]byte, 32)

	// What siwc.Session.Import stores, without the signed ID token it checks.
	raw, _ := json.Marshal(siwc.Credentials{ClientID: "c", RefreshToken: "r", IDToken: "i", Scope: siwc.PlanScope,
		ExpiresAt: time.Now().Add(time.Hour), Email: "ana@example.com"})
	k := store.UserKey("ana", "siwc.credentials")
	sealed, err := store.Seal(key, raw, k)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetSetting(context.Background(), db, k, sealed); err != nil {
		t.Fatal(err)
	}

	a := &api{base: &siwc.Session{DB: db, Key: key}, mode: m, providers: &providers.Store{DB: db, Key: key}, usage: &usage.Recorder{DB: db}}
	mux := http.NewServeMux()
	a.routes(&router{mux: mux, acc: &access.Checker{DB: db}})
	mux.HandleFunc("/api/", http.NotFound) // as in main
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "ana"})))
	}), db
}

func call(t *testing.T, h http.Handler, method, path string, out any) int {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader("{}")))
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return w.Code
}

type chatgptStatus struct {
	Status struct {
		SignedIn    bool `json:"signed_in"`
		PlanGranted bool `json:"plan_granted"`
	} `json:"status"`
	AIReady bool `json:"ai_ready"`
}

func TestSaaSHasNoChatGPT(t *testing.T) {
	h := newAIServer(t, mode.SaaS)
	for _, path := range []string{"/api/chatgpt/credentials", "/api/chatgpt/signout"} {
		if code := call(t, h, "POST", path, nil); code != http.StatusNotFound {
			t.Errorf("POST %s = %d, want 404", path, code)
		}
	}
	var st chatgptStatus
	if code := call(t, h, "GET", "/api/chatgpt/status", &st); code != http.StatusOK || st.Status.SignedIn || st.AIReady {
		t.Errorf("status = %d %+v: a stored ChatGPT credential must be ignored", code, st)
	}
	var models []struct{ Provider string }
	if code := call(t, h, "GET", "/api/models", &models); code != http.StatusOK || len(models) != 0 {
		t.Errorf("models = %d %+v, want none", code, models)
	}
	var me struct{ Mode string }
	if call(t, h, "GET", "/api/me", &me); me.Mode != "saas" {
		t.Errorf("me.mode = %q", me.Mode)
	}
}

func TestSelfHostKeepsChatGPT(t *testing.T) {
	h := newAIServer(t, mode.SelfHost)
	// An invalid file is rejected by the handler: the route exists.
	if code := call(t, h, "POST", "/api/chatgpt/credentials", nil); code != http.StatusBadRequest {
		t.Errorf("POST credentials = %d, want 400", code)
	}
	var st chatgptStatus
	if call(t, h, "GET", "/api/chatgpt/status", &st); !st.Status.SignedIn || !st.Status.PlanGranted || !st.AIReady {
		t.Errorf("status = %+v: the stored credential should count", st)
	}
	var me struct{ Mode string }
	if call(t, h, "GET", "/api/me", &me); me.Mode != "selfhost" {
		t.Errorf("me.mode = %q", me.Mode)
	}
}

// GET /api/usage: the signed-in account's tokens per model from since, most
// used first; nobody else's.
func TestUsage(t *testing.T) {
	h, db := newAIServerDB(t, mode.SelfHost)
	ctx := context.Background()
	add := func(at int64, user, model string, in, cached, out, reasoning int) {
		rec := &usage.Recorder{DB: db, Now: func() time.Time { return time.Unix(at, 0) }}
		e := usage.Entry{UserID: user, Kind: "agent", Model: model, Usage: ai.Usage{InputTokens: in, CachedTokens: cached, OutputTokens: out, ReasoningTokens: reasoning}}
		if err := rec.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	add(100, "ana", "small", 9000, 0, 9000, 0) // before since
	add(200, "ana", "small", 10, 5, 20, 0)
	add(300, "ana", "small", 30, 0, 40, 10)
	add(300, "ana", "big", 500, 100, 50, 25)
	add(300, "bob", "small", 7, 0, 8, 0)

	type row struct {
		Model           string `json:"model"`
		Calls           int    `json:"calls"`
		InputTokens     int    `json:"input_tokens"`
		CachedTokens    int    `json:"cached_tokens"`
		OutputTokens    int    `json:"output_tokens"`
		ReasoningTokens int    `json:"reasoning_tokens"`
	}
	var got struct{ Models []row }
	if code := call(t, h, "GET", "/api/usage?since=200", &got); code != http.StatusOK {
		t.Fatalf("GET /api/usage = %d", code)
	}
	want := []row{
		{Model: "big", Calls: 1, InputTokens: 500, CachedTokens: 100, OutputTokens: 50, ReasoningTokens: 25},
		{Model: "small", Calls: 2, InputTokens: 40, CachedTokens: 5, OutputTokens: 60, ReasoningTokens: 10},
	}
	if !reflect.DeepEqual(got.Models, want) {
		t.Fatalf("usage = %+v, want %+v", got.Models, want)
	}

	var none struct{ Models []row }
	if call(t, h, "GET", "/api/usage?since=1000", &none); none.Models == nil || len(none.Models) != 0 {
		t.Fatalf("usage with no calls = %#v, want an empty list", none.Models)
	}
	for _, q := range []string{"", "?since=", "?since=abc", "?since=-1"} {
		if code := call(t, h, "GET", "/api/usage"+q, nil); code != http.StatusBadRequest {
			t.Errorf("GET /api/usage%s = %d, want 400", q, code)
		}
	}
}
