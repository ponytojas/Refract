package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/zendev-sh/goai"
	"github.com/zendev-sh/goai/provider"
	"github.com/zendev-sh/goai/provider/anthropic"
	"github.com/zendev-sh/goai/provider/compat"
	"github.com/zendev-sh/goai/provider/deepseek"
	"github.com/zendev-sh/goai/provider/google"
	"github.com/zendev-sh/goai/provider/groq"
	"github.com/zendev-sh/goai/provider/mistral"
	"github.com/zendev-sh/goai/provider/ollama"
	"github.com/zendev-sh/goai/provider/openai"
	"github.com/zendev-sh/goai/provider/openrouter"
	"github.com/zendev-sh/goai/provider/xai"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/netguard"
)

// Provider calls one configured provider. It implements ai.Provider; model
// ids reach it without the config prefix.
type Provider struct {
	Config Config
	HTTP   *http.Client
}

func (p *Provider) Status(context.Context) ai.Status {
	return ai.Status{Provider: p.Config.Kind, SignedIn: true, PlanGranted: true}
}

func (p *Provider) model(id string) provider.LanguageModel {
	c, hc := p.Config, p.HTTP
	switch c.Kind {
	case "openai":
		return openai.Chat(id, openai.WithAPIKey(c.APIKey), openai.WithBaseURL(c.base()), openai.WithHTTPClient(hc))
	case "anthropic":
		return anthropic.Chat(id, anthropic.WithAPIKey(c.APIKey), anthropic.WithBaseURL(c.base()), anthropic.WithHTTPClient(hc))
	case "google":
		return google.Chat(id, google.WithAPIKey(c.APIKey), google.WithBaseURL(c.base()), google.WithHTTPClient(hc))
	case "openrouter":
		return openrouter.Chat(id, openrouter.WithAPIKey(c.APIKey), openrouter.WithBaseURL(c.base()), openrouter.WithHTTPClient(hc))
	case "mistral":
		return mistral.Chat(id, mistral.WithAPIKey(c.APIKey), mistral.WithBaseURL(c.base()), mistral.WithHTTPClient(hc))
	case "deepseek":
		return deepseek.Chat(id, deepseek.WithAPIKey(c.APIKey), deepseek.WithBaseURL(c.base()), deepseek.WithHTTPClient(hc))
	case "xai":
		return xai.Chat(id, xai.WithAPIKey(c.APIKey), xai.WithBaseURL(c.base()), xai.WithHTTPClient(hc))
	case "groq":
		return groq.Chat(id, groq.WithAPIKey(c.APIKey), groq.WithBaseURL(c.base()), groq.WithHTTPClient(hc))
	case "ollama":
		return ollama.Chat(id, ollama.WithBaseURL(c.base()), ollama.WithHTTPClient(hc))
	default: // compat
		opts := []compat.Option{compat.WithBaseURL(c.base()), compat.WithHTTPClient(hc), compat.WithProviderID(c.ID)}
		if c.APIKey != "" {
			opts = append(opts, compat.WithAPIKey(c.APIKey))
		}
		return compat.Chat(id, opts...)
	}
}

// effortOptions maps a reasoning level (see effortsFor) to each API's own
// setting. summary asks for readable reasoning where it is optional.
func effortOptions(kind, model, effort string, summary bool) (opts map[string]any, maxTokens int) {
	opts = map[string]any{}
	if kind == "openai" {
		opts["store"] = false // stateless: reasoning comes back encrypted and is replayed
	}
	if effort == "" || effort == "none" {
		return opts, 0
	}
	switch kind {
	case "openai":
		opts["reasoning_effort"] = effort
		if summary {
			opts["reasoning_summary"] = "auto"
		}
	case "openrouter":
		opts["reasoning"] = map[string]any{"effort": effort}
	case "anthropic":
		budget := map[string]int{"low": 2048, "medium": 8192, "high": 24576}[effort]
		opts["thinking"] = map[string]any{"type": "enabled", "budgetTokens": budget}
		return opts, budget + 16384 // max_tokens must leave room for the answer
	case "google":
		tc := map[string]any{"includeThoughts": summary}
		if strings.HasPrefix(model, "gemini-2.5") {
			tc["thinkingBudget"] = map[string]int{"low": 1024, "medium": 8192, "high": 24576}[effort]
		} else {
			tc["thinkingLevel"] = effort
		}
		opts["google"] = map[string]any{"thinkingConfig": tc}
	}
	return opts, 0
}

// itemType tags the history items this adapter writes: the whole assistant
// message of a step, so reasoning (signatures, encrypted content) replays.
const itemType = "goai.message"

func (p *Provider) Stream(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	// GoAI falls back to env keys (OPENAI_API_KEY…) when none is given: never
	// let a user's call spend the server operator's key.
	if k, _ := kind(p.Config.Kind); k.NeedsKey && p.Config.APIKey == "" {
		return nil, &ai.Error{Kind: ai.ErrInvalidKey, Message: k.Name + " needs an API key"}
	}
	msgs, err := toMessages(req.Input)
	if err != nil {
		return nil, err
	}
	effort, summary := "", false
	if req.Reasoning != nil {
		effort, summary = req.Reasoning.Effort, req.Reasoning.Summary != ""
	}
	ts, err := p.start(ctx, req, msgs, effort, summary)
	// The model doesn't take part of the reasoning setting: drop the summary
	// (OpenAI requires a verified organization for it), then the level.
	if err != nil && summary && effort != "" && badRequest(err) {
		ts, err = p.start(ctx, req, msgs, effort, false)
	}
	if err != nil && effort != "" && badRequest(err) {
		ts, err = p.start(ctx, req, msgs, "", false)
	}
	if err != nil {
		return nil, mapError(err, p.Config)
	}
	ch := make(chan ai.Event)
	go func() {
		defer close(ch)
		send := func(ev ai.Event) bool {
			select {
			case ch <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for chunk := range ts.Stream() {
			switch chunk.Type {
			case provider.ChunkText:
				if !send(ai.Event{Type: ai.EventText, Text: chunk.Text}) {
					return
				}
			case provider.ChunkReasoning:
				if !send(ai.Event{Type: ai.EventReasoning, Text: chunk.Text}) {
					return
				}
			case provider.ChunkError:
				send(ai.Event{Type: ai.EventError, Err: mapError(chunk.Error, p.Config)})
				return
			}
		}
		if err := ts.Err(); err != nil {
			send(ai.Event{Type: ai.EventError, Err: mapError(err, p.Config)})
			return
		}
		res := ts.Result()
		for _, m := range res.ResponseMessages {
			item, _ := json.Marshal(map[string]any{"type": itemType, "message": m})
			if !send(ai.Event{Type: ai.EventItem, Item: item}) {
				return
			}
		}
		for _, c := range res.ToolCalls {
			call := &ai.FunctionCall{CallID: c.ID, Name: c.Name, Arguments: string(c.Input)}
			if !send(ai.Event{Type: ai.EventItem, Call: call}) {
				return
			}
		}
		u := res.TotalUsage
		send(ai.Event{Type: ai.EventCompleted, Usage: &ai.Usage{InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, TotalTokens: u.TotalTokens}})
	}()
	return ch, nil
}

func (p *Provider) start(ctx context.Context, req ai.Request, msgs []provider.Message, effort string, summary bool) (*goai.TextStream, error) {
	popts, maxTokens := effortOptions(p.Config.Kind, req.Model, effort, summary)
	opts := []goai.Option{
		goai.WithMessages(msgs...),
		goai.WithMaxSteps(1),   // our agent runs the tool loop
		goai.WithMaxRetries(0), // and decides about retries
		goai.WithProviderOptions(popts),
	}
	if req.Instructions != "" {
		opts = append(opts, goai.WithSystem(req.Instructions))
	}
	if maxTokens > 0 {
		opts = append(opts, goai.WithMaxOutputTokens(maxTokens))
	}
	if len(req.Tools) > 0 {
		tools := make([]goai.Tool, len(req.Tools))
		for i, t := range req.Tools {
			tools[i] = goai.Tool{Name: t.Name, Description: t.Description, InputSchema: t.Parameters} // no Execute: we run tools
		}
		opts = append(opts, goai.WithTools(tools...))
	}
	return goai.StreamText(ctx, p.model(req.Model), opts...)
}

// toMessages turns our history (Responses-style items) into GoAI messages:
// user and assistant text, our own step messages, and tool results.
func toMessages(input []json.RawMessage) ([]provider.Message, error) {
	var out []provider.Message
	names := map[string]string{} // tool call id → tool name (Gemini needs it on results)
	for _, raw := range input {
		var it struct {
			Type    string           `json:"type"`
			Role    string           `json:"role"`
			Content json.RawMessage  `json:"content"`
			CallID  string           `json:"call_id"`
			Output  string           `json:"output"`
			Message provider.Message `json:"message"`
		}
		if err := json.Unmarshal(raw, &it); err != nil {
			return nil, fmt.Errorf("history item: %w", err)
		}
		switch {
		case it.Type == itemType:
			for _, part := range it.Message.Content {
				if part.Type == provider.PartToolCall {
					names[part.ToolCallID] = part.ToolName
				}
			}
			out = append(out, it.Message)
		case it.Type == "function_call_output":
			result := provider.Part{Type: provider.PartToolResult, ToolCallID: it.CallID, ToolName: names[it.CallID], ToolOutput: it.Output}
			// Results of one step's calls go together in one message.
			if n := len(out); n > 0 && out[n-1].Role == provider.RoleTool {
				out[n-1].Content = append(out[n-1].Content, result)
			} else {
				out = append(out, provider.Message{Role: provider.RoleTool, Content: []provider.Part{result}})
			}
		case it.Role == "user" || it.Role == "assistant":
			var text string
			if err := json.Unmarshal(it.Content, &text); err != nil {
				continue // structured content from another provider's items: not ours to replay
			}
			out = append(out, provider.Message{Role: provider.Role(it.Role), Content: []provider.Part{{Type: provider.PartText, Text: text}}})
		}
		// Other items (another provider's reasoning, for instance) are skipped.
	}
	return out, nil
}

func badRequest(err error) bool {
	var ae *goai.APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusBadRequest
}

var quotaBody = regexp.MustCompile(`(?i)insufficient_quota|credit|billing|quota`)

// mapError gives provider errors the kinds the UI explains.
func mapError(err error, c Config) *ai.Error {
	if err == nil {
		return nil
	}
	var ae *ai.Error
	if errors.As(err, &ae) {
		return ae
	}
	k, _ := kind(c.Kind)
	var api *goai.APIError
	var overflow *goai.ContextOverflowError
	switch {
	case errors.Is(err, context.Canceled):
		return &ai.Error{Kind: ai.ErrOther, Message: "stopped"}
	case errors.Is(err, netguard.ErrPrivate):
		return &ai.Error{Kind: ai.ErrOther, Message: k.Name + ": " + netguard.ErrPrivate.Error()}
	case errors.As(err, &overflow):
		return &ai.Error{Kind: ai.ErrOther, Message: "The conversation is too long for this model: start a new chat."}
	case errors.As(err, &api):
		e := &ai.Error{Kind: ai.ErrOther, Status: api.StatusCode, Message: strings.TrimSpace(k.Name + ": " + api.Message)}
		switch {
		case api.StatusCode == http.StatusUnauthorized || api.StatusCode == http.StatusForbidden:
			e.Kind = ai.ErrInvalidKey
		case api.StatusCode == http.StatusPaymentRequired || quotaBody.MatchString(api.ResponseBody) && api.StatusCode == http.StatusTooManyRequests:
			e.Kind = ai.ErrQuota
		case api.StatusCode == http.StatusTooManyRequests || api.StatusCode >= 500:
			e.Kind = ai.ErrUnavailable
		case api.StatusCode == http.StatusNotFound:
			e.Kind = ai.ErrModelUnavailable
		}
		return e
	}
	return &ai.Error{Kind: ai.ErrUnavailable, Message: k.Name + ": " + err.Error()}
}
