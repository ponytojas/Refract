// Package ai is the provider-agnostic layer over the Responses API.
package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Provider streams Responses API calls. How credentials are obtained is the
// implementation's business (SIWC login today, API keys later).
type Provider interface {
	Stream(ctx context.Context, req Request) (<-chan Event, error)
	Status(ctx context.Context) Status
}

// Request is the subset of a Responses API request every provider supports.
// Input items are raw so output items can be fed back verbatim on the next
// call (the plan route is stateless: no previous_response_id).
type Request struct {
	Model        string            `json:"model"`
	Instructions string            `json:"instructions,omitempty"`
	Input        []json.RawMessage `json:"input"`
	Tools        []Tool            `json:"tools,omitempty"`
	Reasoning    *Reasoning        `json:"reasoning,omitempty"`
}

// Reasoning sets how hard the model thinks: one of the model's Efforts.
// Summary "auto" asks for a readable summary of the reasoning (EventReasoning).
type Reasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

// Tool is a function tool definition.
type Tool struct {
	Type        string          `json:"type"` // "function"
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

// UserMessage builds a user input item.
func UserMessage(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"role": "user", "content": text})
	return b
}

// FunctionOutput builds the input item answering a function call.
func FunctionOutput(callID, output string) json.RawMessage {
	b, _ := json.Marshal(map[string]string{"type": "function_call_output", "call_id": callID, "output": output})
	return b
}

// EventType tags Event.
type EventType int

const (
	EventText      EventType = iota // Text holds a delta
	EventItem                       // Item holds a finished output item; Call is set for function calls
	EventCompleted                  // the only success signal; Usage is set
	EventError                      // Err is set; the stream ends
	EventReasoning                  // Text holds a delta of the model's reasoning (summary), shown apart from the answer
)

// Event is one streamed event. The channel closes after EventCompleted or EventError.
type Event struct {
	Type  EventType
	Text  string
	Item  json.RawMessage
	Call  *FunctionCall
	Usage *Usage
	Err   *Error
}

// FunctionCall is a tool call the model asked for.
type FunctionCall struct {
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Usage is token accounting from response.completed. CachedTokens is the
// part of InputTokens served from the provider's cache, and ReasoningTokens
// the part of OutputTokens spent reasoning; both are 0 when unknown.
type Usage struct {
	InputTokens     int `json:"input_tokens"`
	OutputTokens    int `json:"output_tokens"`
	TotalTokens     int `json:"total_tokens"`
	CachedTokens    int `json:"cached_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

// UnmarshalJSON reads the Responses API's usage object, where the two
// details are nested.
func (u *Usage) UnmarshalJSON(b []byte) error {
	var raw struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
		In           struct {
			Cached int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		Out struct {
			Reasoning int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	*u = Usage{
		InputTokens:     raw.InputTokens,
		OutputTokens:    raw.OutputTokens,
		TotalTokens:     raw.TotalTokens,
		CachedTokens:    raw.In.Cached,
		ReasoningTokens: raw.Out.Reasoning,
	}
	return nil
}

// Status is what the UI shows about a provider.
type Status struct {
	Provider     string `json:"provider"`
	SignedIn     bool   `json:"signed_in"`
	PlanGranted  bool   `json:"plan_granted"`
	Email        string `json:"email,omitempty"`
	LimitReached bool   `json:"limit_reached"`
}

// ErrorKind is what the UI needs to tell the user.
type ErrorKind string

const (
	ErrNotSignedIn      ErrorKind = "not_signed_in"
	ErrPlanNotGranted   ErrorKind = "plan_not_granted"
	ErrNotEligible      ErrorKind = "not_eligible"
	ErrUsageLimit       ErrorKind = "usage_limit"
	ErrModelUnavailable ErrorKind = "model_unavailable"
	ErrUnsupported      ErrorKind = "unsupported"
	ErrUnavailable      ErrorKind = "unavailable" // transient: retry later
	ErrInvalidKey       ErrorKind = "invalid_key" // an API provider rejected the key
	ErrQuota            ErrorKind = "quota"       // an API provider's credit ran out
	ErrOther            ErrorKind = "other"
)

// Error is a provider error the UI can act on.
type Error struct {
	Kind    ErrorKind `json:"kind"`
	Code    string    `json:"code,omitempty"`
	Param   string    `json:"param,omitempty"`
	Message string    `json:"message"`
	Status  int       `json:"status,omitempty"`
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s (%s %d): %s", e.Kind, e.Code, e.Status, e.Message)
}

// Complete runs one request without tools and returns the model's text.
func Complete(ctx context.Context, p Provider, req Request) (string, error) {
	ch, err := p.Stream(ctx, req)
	if err != nil {
		return "", err
	}
	var text strings.Builder
	for ev := range ch {
		switch ev.Type {
		case EventText:
			text.WriteString(ev.Text)
		case EventError:
			return "", ev.Err
		case EventCompleted:
			return text.String(), nil
		}
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", errors.New("the model stopped before finishing")
}
