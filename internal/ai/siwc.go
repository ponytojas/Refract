package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	"github.com/ponytojas/Refract/internal/siwc"
)

// TokenSource is what the SIWC provider needs from siwc.Session.
type TokenSource interface {
	AccessToken(ctx context.Context, force bool) (string, error)
	Credentials(ctx context.Context) (*siwc.Credentials, error)
}

// SIWC calls the public Responses API with the user's ChatGPT plan token.
// Raw HTTP rather than openai-go: the body is a handful of fields, the plan
// route rejects many SDK-level knobs, and its errors come in two shapes
// ({"error":{...}} and {"detail":"..."}) we must map ourselves anyway.
type SIWC struct {
	Tokens  TokenSource
	BaseURL string // https://api.openai.com/v1
	HTTP    *http.Client

	mu           sync.Mutex
	limitReached bool            // set by a usage-limit error, cleared by the next success
	badEfforts   map[string]bool // "model/effort" the API rejected: not offered again
	noSummary    bool            // the route refused reasoning summaries: stop asking
}

// client is p.HTTP with redirects refused, whatever the caller configured: the
// bearer token and body must never be forwarded to another origin.
func (p *SIWC) client() *http.Client {
	c := *p.HTTP
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

func (p *SIWC) Status(ctx context.Context) Status {
	st := Status{Provider: "chatgpt"}
	c, err := p.Tokens.Credentials(ctx)
	if err != nil || c == nil {
		return st
	}
	st.SignedIn = c.RefreshToken != ""
	st.PlanGranted = c.PlanGranted()
	st.Email = c.Email
	p.mu.Lock()
	st.LimitReached = p.limitReached
	p.mu.Unlock()
	return st
}

// Model is one model the signed-in account may use.
type Model struct {
	Slug          string   `json:"slug"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	DefaultEffort string   `json:"default_effort"`
	Efforts       []Effort `json:"efforts"`                 // reasoning levels, lightest first; empty: no reasoning control
	Provider      string   `json:"provider,omitempty"`      // "chatgpt" or a configured provider's id
	ProviderName  string   `json:"provider_name,omitempty"` // shown as the group in the picker
}

// Effort is one reasoning level of a model.
type Effort struct {
	Effort      string `json:"effort"`
	Description string `json:"description"`
}

// clientOnlyEfforts are levels the models list offers for Codex's own client
// ("ultra": client-side task delegation) that the Responses API rejects.
// Anything else listed is offered; a level the API turns down anyway is
// learned at run time (see Stream) and dropped.
var clientOnlyEfforts = map[string]bool{"ultra": true}

func (p *SIWC) offered(model, effort string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !clientOnlyEfforts[effort] && !p.badEfforts[model+"/"+effort]
}

// Models lists models available on the user's plan.
func (p *SIWC) Models(ctx context.Context) ([]Model, error) {
	resp, err := p.do(ctx, "GET", "/models", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var body struct {
		Models []struct {
			Slug        string   `json:"slug"`
			DisplayName string   `json:"display_name"`
			Description string   `json:"description"`
			Visibility  string   `json:"visibility"`
			Default     string   `json:"default_reasoning_level"`
			Levels      []Effort `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	out := []Model{}
	for _, m := range body.Models {
		if m.Visibility != "list" {
			continue
		}
		mm := Model{Slug: m.Slug, DisplayName: m.DisplayName, Description: m.Description, Efforts: []Effort{}}
		for _, l := range m.Levels {
			if p.offered(m.Slug, l.Effort) {
				mm.Efforts = append(mm.Efforts, l)
			}
		}
		if mm.HasEffort(m.Default) {
			mm.DefaultEffort = m.Default
		}
		out = append(out, mm)
	}
	return out, nil
}

// HasEffort reports whether e is one of the model's reasoning levels.
func (m Model) HasEffort(e string) bool {
	for _, x := range m.Efforts {
		if x.Effort == e {
			return true
		}
	}
	return false
}

// Pick resolves a requested model and effort against the plan's models: an
// empty slug means the first model, an unknown or empty effort the model's
// default. ok is false when slug is set but not offered.
func Pick(ms []Model, slug, effort string) (m Model, eff string, ok bool) {
	for _, x := range ms {
		if x.Slug == slug || slug == "" {
			m, ok = x, true
			break
		}
	}
	if !ok {
		return Model{}, "", false
	}
	if m.HasEffort(effort) {
		return m, effort, true
	}
	return m, m.DefaultEffort, true
}

func (p *SIWC) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	// Plan route requirements: stateless and streamed. Reasoning items come
	// back encrypted so they can be replayed in the next call's input.
	p.mu.Lock()
	if p.noSummary && req.Reasoning != nil && req.Reasoning.Summary != "" {
		r := *req.Reasoning
		r.Summary = ""
		req.Reasoning = &r
	}
	p.mu.Unlock()
	body, err := json.Marshal(struct {
		Request
		Store   bool     `json:"store"`
		Stream  bool     `json:"stream"`
		Include []string `json:"include"`
	}{req, false, true, []string{"reasoning.encrypted_content"}})
	if err != nil {
		return nil, err
	}
	resp, err := p.do(ctx, "POST", "/responses", body)
	var ae *Error
	if errors.As(err, &ae) && ae.Param == "reasoning.summary" && req.Reasoning != nil {
		p.mu.Lock()
		p.noSummary = true
		p.mu.Unlock()
		return p.Stream(ctx, req)
	}
	if errors.As(err, &ae) && ae.Param == "reasoning.effort" && req.Reasoning != nil {
		// A level the models list offers but the API refuses: remember it and
		// answer with the model's default level instead of failing the request.
		p.mu.Lock()
		if p.badEfforts == nil {
			p.badEfforts = map[string]bool{}
		}
		p.badEfforts[req.Model+"/"+req.Reasoning.Effort] = true
		p.mu.Unlock()
		if req.Reasoning.Summary != "" {
			req.Reasoning = &Reasoning{Summary: req.Reasoning.Summary}
		} else {
			req.Reasoning = nil
		}
		return p.Stream(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	ch := make(chan Event)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		p.readStream(ctx, resp.Body, ch)
	}()
	return ch, nil
}

// do sends an authenticated request and returns a 200 response or an *Error.
// A 401 gets one forced refresh and retry.
func (p *SIWC) do(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	c, err := p.Tokens.Credentials(ctx)
	if err != nil {
		return nil, &Error{Kind: ErrOther, Message: err.Error()}
	}
	if c == nil || c.RefreshToken == "" {
		return nil, &Error{Kind: ErrNotSignedIn, Message: "Sign in with ChatGPT: run `refract login` and import the credentials."}
	}
	if !c.PlanGranted() {
		return nil, &Error{Kind: ErrPlanNotGranted, Message: "ChatGPT plan usage was not granted. Run `refract login` again and allow plan usage."}
	}
	for attempt := 0; ; attempt++ {
		token, err := p.Tokens.AccessToken(ctx, attempt > 0)
		if errors.Is(err, siwc.ErrNotSignedIn) {
			return nil, &Error{Kind: ErrNotSignedIn, Message: "Your ChatGPT session ended. Run `refract login` again and import the credentials."}
		}
		if err != nil {
			return nil, &Error{Kind: ErrUnavailable, Message: err.Error()}
		}
		req, err := http.NewRequestWithContext(ctx, method, p.BaseURL+path, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := p.client().Do(req)
		if err != nil {
			return nil, &Error{Kind: ErrUnavailable, Message: err.Error()}
		}
		if resp.StatusCode == http.StatusOK {
			return resp, nil
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			continue
		}
		e := parseError(resp.StatusCode, raw)
		p.noteLimit(e)
		return nil, e
	}
}

func (p *SIWC) readStream(ctx context.Context, r io.Reader, ch chan<- Event) {
	send := func(ev Event) bool {
		select {
		case ch <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 16<<20) // output items with encrypted reasoning can be large
	var data strings.Builder
	for sc.Scan() {
		line := sc.Text()
		if d, ok := strings.CutPrefix(line, "data:"); ok {
			data.WriteString(strings.TrimPrefix(d, " "))
			continue
		}
		if line != "" || data.Len() == 0 {
			continue // event:/id: lines; the type is also inside data
		}
		ev, final := parseEvent([]byte(data.String()))
		data.Reset()
		if ev == nil {
			continue
		}
		if ev.Err != nil {
			p.noteLimit(ev.Err)
		} else if ev.Type == EventCompleted {
			p.mu.Lock()
			p.limitReached = false
			p.mu.Unlock()
		}
		if !send(*ev) || final {
			return
		}
	}
	msg := "stream ended before response.completed"
	if err := sc.Err(); err != nil {
		msg += ": " + err.Error()
	}
	send(Event{Type: EventError, Err: &Error{Kind: ErrUnavailable, Message: msg}})
}

func (p *SIWC) noteLimit(e *Error) {
	if e.Kind == ErrUsageLimit {
		p.mu.Lock()
		p.limitReached = true
		p.mu.Unlock()
	}
}

// parseEvent turns one SSE data payload into an Event; final means the stream is over.
func parseEvent(data []byte) (ev *Event, final bool) {
	var e struct {
		Type     string          `json:"type"`
		Delta    string          `json:"delta"`
		Item     json.RawMessage `json:"item"`
		Code     string          `json:"code"`
		Message  string          `json:"message"`
		Param    string          `json:"param"`
		Response struct {
			Usage             *Usage                   `json:"usage"`
			Error             *apiError                `json:"error"`
			IncompleteDetails *struct{ Reason string } `json:"incomplete_details"`
		} `json:"response"`
	}
	if json.Unmarshal(data, &e) != nil {
		return nil, false
	}
	switch e.Type {
	case "response.output_text.delta":
		return &Event{Type: EventText, Text: e.Delta}, false
	case "response.reasoning_summary_text.delta":
		return &Event{Type: EventReasoning, Text: e.Delta}, false
	case "response.reasoning_summary_part.done":
		return &Event{Type: EventReasoning, Text: "\n\n"}, false // paragraphs of the summary
	case "response.output_item.done":
		ev := &Event{Type: EventItem, Item: e.Item}
		var it struct {
			Type string `json:"type"`
			FunctionCall
		}
		if json.Unmarshal(e.Item, &it) == nil && it.Type == "function_call" {
			ev.Call = &it.FunctionCall
		}
		return ev, false
	case "response.completed":
		return &Event{Type: EventCompleted, Usage: e.Response.Usage}, true
	case "response.failed":
		ae := apiError{Message: "response failed"}
		if e.Response.Error != nil {
			ae = *e.Response.Error
		}
		return &Event{Type: EventError, Err: classify(0, ae)}, true
	case "response.incomplete":
		reason := "unknown"
		if d := e.Response.IncompleteDetails; d != nil {
			reason = d.Reason
		}
		return &Event{Type: EventError, Err: &Error{Kind: ErrOther, Code: "incomplete", Message: "response incomplete: " + reason}}, true
	case "error":
		return &Event{Type: EventError, Err: classify(0, apiError{Code: e.Code, Message: e.Message, Param: e.Param})}, true
	}
	return nil, false
}

type apiError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
}

// parseError reads a non-200 body: {"error":{...}} or {"detail":"..."}.
func parseError(status int, body []byte) *Error {
	var b struct {
		Error  *apiError `json:"error"`
		Detail string    `json:"detail"`
	}
	json.Unmarshal(body, &b)
	ae := apiError{Message: b.Detail}
	if b.Error != nil {
		ae = *b.Error
	}
	if ae.Message == "" {
		ae.Message = fmt.Sprintf("HTTP %d: %s", status, bytes.TrimSpace(body))
	}
	return classify(status, ae)
}

// classify maps documented SIWC error codes (errors-and-recovery) to UI kinds.
func classify(status int, ae apiError) *Error {
	e := &Error{Kind: ErrOther, Code: ae.Code, Param: ae.Param, Message: ae.Message, Status: status}
	switch ae.Code {
	case "subscription_sharing_usage_limit_exceeded":
		e.Kind = ErrUsageLimit
	case "subscription_sharing_user_not_eligible":
		e.Kind = ErrNotEligible
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		e.Kind = ErrUnavailable
	case "subscription_sharing_unsupported_capability":
		e.Kind = ErrUnsupported
		if ae.Param == "model" {
			e.Kind = ErrModelUnavailable
		}
	case "":
		switch {
		case status == http.StatusForbidden:
			e.Kind = ErrNotEligible // policy/region admission check
		case status == http.StatusTooManyRequests:
			// Never retried: the plan's usage limit. The UI links to Manage usage.
			e.Kind = ErrUsageLimit
		case status >= 500:
			e.Kind = ErrUnavailable
		}
	}
	return e
}
