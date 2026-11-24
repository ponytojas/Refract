package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/mode"
)

func send(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	return w
}

func offersOllama(t *testing.T, h http.Handler) bool {
	t.Helper()
	var out struct {
		Kinds []struct {
			ID string `json:"id"`
		} `json:"kinds"`
	}
	w := send(t, h, "GET", "/api/providers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/providers: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, k := range out.Kinds {
		if k.ID == "ollama" {
			return true
		}
	}
	return false
}

func TestOutboundPublicProvidersMustBePublic(t *testing.T) {
	h, _ := newAIServerOutbound(t, mode.SelfHost, true)
	if offersOllama(t, h) {
		t.Error("the hosted service offers Ollama")
	}
	for _, body := range []string{
		`{"kind":"ollama"}`,
		`{"kind":"compat","base_url":"http://api.example.com/v1"}`,
		`{"kind":"compat","base_url":"https://10.0.0.5/v1"}`,
		`{"kind":"openai","base_url":"https://127.0.0.1/v1"}`,
	} {
		if w := send(t, h, "PUT", "/api/providers/p", body); w.Code != http.StatusBadRequest {
			t.Errorf("saving %s: %d, want 400", body, w.Code)
		}
	}
	if w := send(t, h, "PUT", "/api/providers/p", `{"kind":"compat","base_url":"https://api.example.com/v1"}`); w.Code != http.StatusOK {
		t.Errorf("a public https base URL: %d %s", w.Code, w.Body)
	}
}

// REFRACT_MODE=saas no longer decides the outbound policy.
func TestSaaSModeDoesNotRestrictOutbound(t *testing.T) {
	h, _ := newAIServerOutbound(t, mode.SaaS, false)
	if !offersOllama(t, h) {
		t.Error("REFRACT_OUTBOUND=any hides Ollama in saas mode")
	}
}

func TestOutboundAnyKeepsLocalModels(t *testing.T) {
	h, _ := newAIServerOutbound(t, mode.SelfHost, false)
	if w := send(t, h, "PUT", "/api/providers/lan", `{"kind":"compat","base_url":"http://10.0.0.5:8000/v1"}`); w.Code != http.StatusOK {
		t.Errorf("private http base URL: %d %s", w.Code, w.Body)
	}
	if !offersOllama(t, h) {
		t.Error("self-hosted doesn't offer Ollama")
	}
	if w := send(t, h, "PUT", "/api/providers/local", `{"kind":"ollama"}`); w.Code != http.StatusOK {
		t.Errorf("saving Ollama: %d %s", w.Code, w.Body)
	}
}
