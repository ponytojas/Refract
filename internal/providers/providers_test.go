package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zendev-sh/goai/provider"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/store"
)

func TestStoreSealsAndKeepsKey(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	s := &Store{DB: db, Key: make([]byte, 32)}
	ctx := context.Background()
	if err := s.Save(ctx, "u1", Config{ID: "oa", Kind: "openai"}); err == nil {
		t.Fatal("saved an OpenAI provider without a key")
	}
	if err := s.Save(ctx, "u1", Config{ID: "oa", Kind: "openai", APIKey: "sk-secret"}); err != nil {
		t.Fatal(err)
	}
	raw, _ := store.GetSetting(ctx, db, storeKey("u1"))
	if strings.Contains(raw, "sk-secret") {
		t.Fatal("API key stored in clear")
	}
	if err := s.Save(ctx, "u1", Config{ID: "oa", Kind: "openai", Name: "Work"}); err != nil {
		t.Fatal(err)
	}
	cs, _ := s.List(ctx, "u1")
	if len(cs) != 1 || cs[0].APIKey != "sk-secret" || cs[0].Name != "Work" {
		t.Fatalf("got %+v", cs)
	}
	if other, _ := s.List(ctx, "u2"); len(other) != 0 {
		t.Fatal("another user sees the provider")
	}
	if err := s.Save(ctx, "u1", Config{ID: "x", Kind: "compat", BaseURL: "http://user:pw@host"}); err == nil {
		t.Fatal("accepted a base URL with credentials")
	}
	s.Delete(ctx, "u1", "oa")
	if cs, _ := s.List(ctx, "u1"); len(cs) != 0 {
		t.Fatal("not deleted")
	}
}

func TestToMessages(t *testing.T) {
	step, _ := json.Marshal(map[string]any{"type": itemType, "message": provider.Message{
		Role: provider.RoleAssistant,
		Content: []provider.Part{
			{Type: provider.PartReasoning, Text: "hmm"},
			{Type: provider.PartToolCall, ToolCallID: "c1", ToolName: "cite", ToolInput: json.RawMessage(`{}`)},
			{Type: provider.PartToolCall, ToolCallID: "c2", ToolName: "search_papers", ToolInput: json.RawMessage(`{}`)},
		},
	}})
	msgs, err := toMessages([]json.RawMessage{
		ai.UserMessage("hi"),
		json.RawMessage(`{"role":"assistant","content":"hello"}`),
		json.RawMessage(`{"type":"reasoning","encrypted_content":"x"}`), // another provider's item
		step,
		ai.FunctionOutput("c1", "one"),
		ai.FunctionOutput("c2", "two"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 4 {
		t.Fatalf("want 4 messages, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != provider.RoleUser || msgs[0].Content[0].Text != "hi" || msgs[1].Content[0].Text != "hello" {
		t.Fatalf("text messages: %+v", msgs[:2])
	}
	if len(msgs[2].Content) != 3 || msgs[2].Content[0].Text != "hmm" {
		t.Fatalf("step not replayed verbatim: %+v", msgs[2])
	}
	res := msgs[3]
	if res.Role != provider.RoleTool || len(res.Content) != 2 || res.Content[1].ToolName != "search_papers" || res.Content[1].ToolOutput != "two" {
		t.Fatalf("tool results: %+v", res)
	}
}

// fakeChat answers OpenAI-style streamed chat completions.
func fakeChat(t *testing.T, status int, chunks ...string) (*httptest.Server, *[]map[string]any) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &body)
		bodies = append(bodies, body)
		if status != http.StatusOK {
			w.WriteHeader(status)
			fmt.Fprint(w, `{"error":{"message":"nope"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", c)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	return srv, &bodies
}

func collect(t *testing.T, p *Provider, req ai.Request) ([]ai.Event, error) {
	ch, err := p.Stream(context.Background(), req)
	if err != nil {
		return nil, err
	}
	var evs []ai.Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs, nil
}

func TestStreamTextAndToolCall(t *testing.T) {
	srv, bodies := fakeChat(t, http.StatusOK,
		`{"choices":[{"index":0,"delta":{"role":"assistant","content":"Let me "}}]}`,
		`{"choices":[{"index":0,"delta":{"content":"look."}}]}`,
		`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"cite","arguments":"{\"q\":\"x\"}"}}]}}]}`,
		`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`,
	)
	p := &Provider{Config: Config{ID: "local", Kind: "compat", BaseURL: srv.URL}, HTTP: srv.Client()}
	evs, err := collect(t, p, ai.Request{
		Model:        "m",
		Instructions: "be brief",
		Input:        []json.RawMessage{ai.UserMessage("hi")},
		Tools:        []ai.Tool{{Type: "function", Name: "cite", Parameters: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var text strings.Builder
	var calls []*ai.FunctionCall
	var items int
	var done *ai.Usage
	for _, ev := range evs {
		switch ev.Type {
		case ai.EventText:
			text.WriteString(ev.Text)
		case ai.EventItem:
			if len(ev.Item) > 0 {
				items++
			}
			if ev.Call != nil {
				calls = append(calls, ev.Call)
			}
		case ai.EventCompleted:
			done = ev.Usage
		case ai.EventError:
			t.Fatal(ev.Err)
		}
	}
	if text.String() != "Let me look." {
		t.Fatalf("text %q", text.String())
	}
	if len(calls) != 1 || calls[0].CallID != "call_1" || calls[0].Name != "cite" || calls[0].Arguments != `{"q":"x"}` {
		t.Fatalf("calls %+v", calls)
	}
	if items == 0 || done == nil || done.TotalTokens != 15 {
		t.Fatalf("items %d usage %+v", items, done)
	}
	if body := (*bodies)[0]; body["model"] != "m" || !strings.Contains(fmt.Sprint(body["messages"]), "be brief") {
		t.Fatalf("request body %v", body)
	}
}

func TestStreamErrorKinds(t *testing.T) {
	for status, want := range map[int]ai.ErrorKind{
		http.StatusUnauthorized:        ai.ErrInvalidKey,
		http.StatusPaymentRequired:     ai.ErrQuota,
		http.StatusNotFound:            ai.ErrModelUnavailable,
		http.StatusServiceUnavailable:  ai.ErrUnavailable,
		http.StatusUnprocessableEntity: ai.ErrOther,
	} {
		srv, _ := fakeChat(t, status)
		p := &Provider{Config: Config{ID: "local", Kind: "compat", BaseURL: srv.URL}, HTTP: srv.Client()}
		evs, err := collect(t, p, ai.Request{Model: "m", Input: []json.RawMessage{ai.UserMessage("hi")}})
		var ae *ai.Error
		if err == nil {
			for _, ev := range evs {
				if ev.Type == ai.EventError {
					ae = ev.Err
				}
			}
		} else if !errors.As(err, &ae) {
			t.Fatalf("%d: %v", status, err)
		}
		if ae == nil || ae.Kind != want {
			t.Errorf("HTTP %d: got %+v, want %s", status, ae, want)
		}
	}
}

func TestStreamRetriesWithoutEffort(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 { // the model turns the reasoning setting down
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":{"message":"unsupported parameter"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	p := &Provider{Config: Config{ID: "c", Kind: "compat", BaseURL: srv.URL}, HTTP: srv.Client()}
	evs, err := collect(t, p, ai.Request{Model: "m", Input: []json.RawMessage{ai.UserMessage("hi")}, Reasoning: &ai.Reasoning{Effort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	if last := evs[len(evs)-1]; last.Type != ai.EventCompleted || calls != 2 {
		t.Fatalf("calls %d, last event %+v", calls, last)
	}
}

func TestEffortOptions(t *testing.T) {
	if o, _ := effortOptions("openai", "gpt-5", "high", true); o["reasoning_effort"] != "high" || o["store"] != false || o["reasoning_summary"] != "auto" {
		t.Fatalf("openai %v", o)
	}
	if _, max := effortOptions("anthropic", "claude-sonnet-4-5", "low", true); max <= 2048 {
		t.Fatal("anthropic max_tokens must exceed the thinking budget")
	}
	if o, _ := effortOptions("anthropic", "claude-sonnet-4-5", "none", true); o["thinking"] != nil {
		t.Fatal("none must not enable thinking")
	}
	g := func(model string) map[string]any {
		o, _ := effortOptions("google", model, "high", true)
		return o["google"].(map[string]any)["thinkingConfig"].(map[string]any)
	}
	if tc := g("gemini-2.5-pro"); tc["thinkingBudget"] != 24576 || tc["thinkingLevel"] != nil {
		t.Fatalf("gemini 2.5 %v", tc)
	}
	if tc := g("gemini-3-pro"); tc["thinkingLevel"] != "high" || tc["includeThoughts"] != true {
		t.Fatalf("gemini 3 %v", tc)
	}
	if o, _ := effortOptions("openrouter", "x", "low", true); fmt.Sprint(o["reasoning"]) != "map[effort:low]" {
		t.Fatalf("openrouter %v", o)
	}
}

func TestEffortsFor(t *testing.T) {
	for _, c := range []struct {
		kind, id string
		reasons  bool
		want     string
	}{
		{"openai", "gpt-5.1", false, "medium"},
		{"openai", "gpt-5-chat-latest", false, ""},
		{"openai", "gpt-4o", false, ""},
		{"openai", "o3", false, "medium"},
		{"anthropic", "claude-sonnet-4-5", false, "none"},
		{"anthropic", "claude-3-5-sonnet-latest", false, ""},
		{"google", "gemini-2.5-flash", false, "medium"},
		{"google", "gemini-2.0-flash", false, ""},
		{"openrouter", "x/y", true, "medium"},
		{"openrouter", "x/z", false, ""},
		{"mistral", "mistral-large", false, ""},
	} {
		efforts, def := effortsFor(c.kind, c.id, c.reasons)
		if def != c.want || (def == "") != (len(efforts) == 0) {
			t.Errorf("%s %s: got %v %q", c.kind, c.id, efforts, def)
		}
	}
}

func TestMissingKeyNeverReachesEnv(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "operator-key")
	p := &Provider{Config: Config{ID: "oa", Kind: "openai"}, HTTP: http.DefaultClient}
	_, err := p.Stream(context.Background(), ai.Request{Model: "gpt", Input: []json.RawMessage{ai.UserMessage("hi")}})
	var ae *ai.Error
	if !errors.As(err, &ae) || ae.Kind != ai.ErrInvalidKey {
		t.Fatalf("got %v", err)
	}
}

func TestListModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags":
			fmt.Fprint(w, `{"models":[{"name":"llama3:8b"},{"name":"qwen3"}]}`)
		case "/v1beta/models":
			if r.Header.Get("x-goog-api-key") != "k" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			fmt.Fprint(w, `{"models":[{"name":"models/gemini-x","displayName":"Gemini X","supportedGenerationMethods":["generateContent"]},{"name":"models/embed","supportedGenerationMethods":["embedContent"]}]}`)
		case "/models":
			fmt.Fprint(w, `{"data":[{"id":"gpt-5"},{"id":"text-embedding-3-large"},{"id":"whisper-1"},{"id":"o3"}]}`)
		}
	}))
	defer srv.Close()
	ctx := context.Background()
	slugs := func(c Config) []string {
		ms, err := ListModels(ctx, srv.Client(), c)
		if err != nil {
			t.Fatalf("%s: %v", c.Kind, err)
		}
		var out []string
		for _, m := range ms {
			out = append(out, m.Slug)
		}
		return out
	}
	if got := fmt.Sprint(slugs(Config{ID: "ol", Kind: "ollama", BaseURL: srv.URL, Models: []string{"extra"}})); got != "[ol:llama3:8b ol:qwen3 ol:extra]" {
		t.Errorf("ollama %s", got)
	}
	if got := fmt.Sprint(slugs(Config{ID: "g", Kind: "google", APIKey: "k", BaseURL: srv.URL})); got != "[g:gemini-x]" {
		t.Errorf("google %s", got)
	}
	if got := fmt.Sprint(slugs(Config{ID: "oa", Kind: "openai", APIKey: "k", BaseURL: srv.URL})); got != "[oa:gpt-5 oa:o3]" {
		t.Errorf("openai %s", got)
	}
	_, err := ListModels(ctx, srv.Client(), Config{ID: "g", Kind: "google", APIKey: "bad", BaseURL: srv.URL})
	var ae *ai.Error
	if !errors.As(err, &ae) || ae.Kind != ai.ErrInvalidKey {
		t.Errorf("bad key: %v", err)
	}
}

func TestRouterRoutesByPrefix(t *testing.T) {
	r := &Router{ChatGPT: &ai.SIWC{}, Configs: func(context.Context) ([]Config, error) {
		return []Config{{ID: "ol", Kind: "ollama"}}, nil
	}}
	p, model, _ := r.route(context.Background(), "ol:llama3:8b")
	if pp, ok := p.(*Provider); !ok || pp.Config.ID != "ol" || model != "llama3:8b" {
		t.Fatalf("got %T %q", p, model)
	}
	for _, slug := range []string{"gpt-5", "nope:model"} {
		if p, model, _ := r.route(context.Background(), slug); p != ai.Provider(r.ChatGPT) || model != slug {
			t.Fatalf("%s: got %T %q", slug, p, model)
		}
	}
}

// Without the ChatGPT plan (REFRACT_MODE=saas) only configured providers are
// reachable: no panic, no ChatGPT models, and a clear error for other slugs.
func TestRouterWithoutChatGPT(t *testing.T) {
	ctx := context.Background()
	r := &Router{Configs: func(context.Context) ([]Config, error) { return nil, nil }}
	if st := r.Status(ctx); st.SignedIn || st.PlanGranted {
		t.Errorf("status = %+v", st)
	}
	if ms, err := r.Models(ctx); err != nil || len(ms) != 0 {
		t.Errorf("models = %v, %v", ms, err)
	}
	for _, slug := range []string{"gpt-5", "nope:model"} {
		_, err := r.Stream(ctx, ai.Request{Model: slug})
		var ae *ai.Error
		if !errors.As(err, &ae) || ae.Kind != ai.ErrNotSignedIn {
			t.Errorf("%s: %v", slug, err)
		}
	}
}
