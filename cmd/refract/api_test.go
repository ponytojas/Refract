package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/providers"
	"github.com/ponytojas/Refract/internal/siwc"
	"github.com/ponytojas/Refract/internal/store"
)

// newAIServer serves the AI routes for one mode, for user "ana", who has a
// ChatGPT credential with plan usage stored from before.
func newAIServer(t *testing.T, m mode.Mode) http.Handler {
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

	a := &api{base: &siwc.Session{DB: db, Key: key}, mode: m, providers: &providers.Store{DB: db, Key: key}}
	mux := http.NewServeMux()
	a.routes(mux)
	mux.HandleFunc("/api/", http.NotFound) // as in main
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "ana"})))
	})
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
