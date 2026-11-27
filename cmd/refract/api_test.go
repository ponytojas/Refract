package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net"
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
	"github.com/ponytojas/Refract/internal/providers"
	"github.com/ponytojas/Refract/internal/siwc"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/usage"
)

// newAIServer serves the AI routes for user "ana", who has a stored ChatGPT credential.
func newAIServer(t *testing.T, publicOnly bool) (http.Handler, *sql.DB) {
	t.Helper()
	return newAIServerWith(t, publicOnly, func(*api) {})
}

// newAIServerWith is newAIServer with the api adjusted by set.
func newAIServerWith(t *testing.T, publicOnly bool, set func(*api)) (http.Handler, *sql.DB) {
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

	a := &api{base: &siwc.Session{DB: db, Key: key}, publicOnly: publicOnly, providers: &providers.Store{DB: db, Key: key}, usage: &usage.Recorder{DB: db}}
	set(a)
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

func TestChatGPTAvailable(t *testing.T) {
	h, _ := newAIServer(t, false)
	// An invalid file is rejected by the handler: the route exists.
	if code := call(t, h, "POST", "/api/chatgpt/credentials", nil); code != http.StatusBadRequest {
		t.Errorf("POST credentials = %d, want 400", code)
	}
	var st chatgptStatus
	if code := call(t, h, "GET", "/api/chatgpt/status", &st); code != http.StatusOK || !st.Status.SignedIn || !st.Status.PlanGranted || !st.AIReady {
		t.Errorf("status = %d %+v: the stored credential should count", code, st)
	}
	var me map[string]any
	if code := call(t, h, "GET", "/api/me", &me); code != http.StatusOK {
		t.Fatalf("GET /api/me = %d, want 200", code)
	}
	if _, ok := me["mode"]; ok {
		t.Error("GET /api/me still reports a deployment mode")
	}
}

// callLocal is call from a browser on this computer, straight to the server.
func callLocal(t *testing.T, h http.Handler, method, path string, out any, header ...string) int {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest(method, path, strings.NewReader("{}"))
	r.RemoteAddr, r.Host = "127.0.0.1:50000", "127.0.0.1:8080"
	for i := 0; i+1 < len(header); i += 2 {
		r.Header.Set(header[i], header[i+1])
	}
	h.ServeHTTP(w, r)
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}
	return w.Code
}

// The in-server ChatGPT sign-in exists only where the server listens on
// loopback (a.logins set); the status says whether it is available.
func TestSignInHereRoutes(t *testing.T) {
	type status struct {
		SignInHere bool `json:"sign_in_here"`
	}
	for _, c := range []struct {
		name   string
		logins bool
		want   bool
	}{
		{"loopback", true, true},
		{"reachable from other computers", false, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			h, _ := newAIServerWith(t, false, func(a *api) {
				if c.logins {
					a.logins = &siwc.Logins{}
				}
			})
			var st status
			if callLocal(t, h, "GET", "/api/chatgpt/status", &st); st.SignInHere != c.want {
				t.Errorf("sign_in_here = %v, want %v", st.SignInHere, c.want)
			}
			code := callLocal(t, h, "GET", "/api/chatgpt/login", nil)
			if want := map[bool]int{true: http.StatusOK, false: http.StatusNotFound}[c.want]; code != want {
				t.Errorf("GET /api/chatgpt/login = %d, want %d", code, want)
			}
		})
	}
}

// A loopback listener behind a reverse proxy serves people on other
// computers: their browser can't reach this computer's callback, so they get
// the credentials file instead of the in-server sign-in.
func TestSignInHereOnlyFromThisComputer(t *testing.T) {
	h, _ := newAIServerWith(t, false, func(a *api) { a.logins = &siwc.Logins{} })
	type status struct {
		SignInHere bool `json:"sign_in_here"`
	}
	var st status
	if callLocal(t, h, "GET", "/api/chatgpt/status", &st); !st.SignInHere {
		t.Fatal("a browser on this computer should sign in here")
	}
	for name, header := range map[string][]string{
		"forwarded for":  {"X-Forwarded-For", "203.0.113.7"},
		"forwarded host": {"X-Forwarded-Host", "refract.example.com"},
		"forwarded":      {"Forwarded", "for=203.0.113.7"},
		"real ip":        {"X-Real-Ip", "203.0.113.7"},
	} {
		st = status{SignInHere: true}
		if callLocal(t, h, "GET", "/api/chatgpt/status", &st, header...); st.SignInHere {
			t.Errorf("%s: sign_in_here = true behind a proxy", name)
		}
		for _, m := range []string{"POST", "GET", "DELETE"} {
			if code := callLocal(t, h, m, "/api/chatgpt/login", nil, header...); code != http.StatusForbidden {
				t.Errorf("%s: %s /api/chatgpt/login = %d, want 403", name, m, code)
			}
		}
	}
	st = status{SignInHere: true}
	if call(t, h, "GET", "/api/chatgpt/status", &st); st.SignInHere {
		t.Error("a request from another address should not sign in here")
	}
}

func TestFromThisComputer(t *testing.T) {
	for _, c := range []struct {
		remote, host string
		want         bool
	}{
		{"127.0.0.1:5000", "127.0.0.1:8080", true},
		{"[::1]:5000", "[::1]:8080", true},
		{"127.0.0.1:5000", "localhost:5173", true},
		{"127.0.0.1:5000", "refract.localhost", true},
		{"127.0.0.1:5000", "refract.example.com", false}, // a proxy on this computer passing the public name
		{"192.168.1.5:5000", "127.0.0.1:8080", false},
		{"bad", "127.0.0.1:8080", false},
	} {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr, r.Host = c.remote, c.host
		if got := fromThisComputer(r); got != c.want {
			t.Errorf("%s → %s: %v, want %v", c.remote, c.host, got, c.want)
		}
	}
}

// Signing out cancels a sign-in still waiting, so it can't store new
// credentials afterwards.
func TestSignOutCancelsPendingSignIn(t *testing.T) {
	revoke := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	t.Cleanup(revoke.Close)
	h, _ := newAIServerWith(t, false, func(a *api) {
		a.base.OAuth = &siwc.OAuth{RevokeURL: revoke.URL, HTTP: revoke.Client()}
		a.logins = &siwc.Logins{Port: -1, AuthorizeURL: "https://auth.invalid/authorize"}
	})
	if code := callLocal(t, h, "POST", "/api/chatgpt/login", nil); code != http.StatusOK {
		t.Fatalf("POST /api/chatgpt/login = %d", code)
	}
	var st siwc.LoginState
	if callLocal(t, h, "GET", "/api/chatgpt/login", &st); st.State != siwc.LoginPending {
		t.Fatalf("state = %+v, want pending", st)
	}
	if code := callLocal(t, h, "POST", "/api/chatgpt/signout", nil); code != http.StatusOK {
		t.Fatalf("POST /api/chatgpt/signout = %d", code)
	}
	if callLocal(t, h, "GET", "/api/chatgpt/login", &st); st.State != siwc.LoginFailed || st.Kind != siwc.FailCancelled {
		t.Errorf("state after sign-out = %+v, want failed/cancelled", st)
	}
}

func TestLoginsOnlyOnLoopback(t *testing.T) {
	for addr, want := range map[string]bool{"127.0.0.1:8080": true, "[::1]:8080": true, "0.0.0.0:8080": false, "192.168.1.5:8080": false} {
		a, _ := net.ResolveTCPAddr("tcp", addr)
		if got := loginsFor(a) != nil; got != want {
			t.Errorf("%s: in-server sign-in = %v, want %v", addr, got, want)
		}
	}
}

// GET /api/usage: the signed-in account's tokens per model from since, most
// used first; nobody else's.
func TestUsage(t *testing.T) {
	h, db := newAIServer(t, false)
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

func TestMeListsMissingTools(t *testing.T) {
	h, _ := newAIServer(t, false)
	var me struct {
		Missing []string `json:"missing_tools"`
	}
	if call(t, h, "GET", "/api/me", &me); me.Missing == nil || len(me.Missing) != 0 {
		t.Errorf("missing_tools = %#v, want []", me.Missing)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/me", nil))
	if !strings.Contains(w.Body.String(), `"missing_tools":[]`) {
		t.Errorf("body = %s", w.Body)
	}
}
