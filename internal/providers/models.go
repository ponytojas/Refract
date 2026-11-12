package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/netguard"
)

var (
	low    = ai.Effort{Effort: "low", Description: "Faster, lighter reasoning"}
	medium = ai.Effort{Effort: "medium", Description: "Balanced"}
	high   = ai.Effort{Effort: "high", Description: "Deeper reasoning"}

	// Anthropic models before extended thinking (Claude 3.7).
	claudeNoThinking = regexp.MustCompile(`^claude-(instant|2|3-(haiku|sonnet|opus|5))`)
)

// effortsFor is what a model offers in the reasoning menu, mapped to each API
// in effortOptions. Models that don't take a reasoning setting offer none
// (they reason, or not, by themselves). reasons is what the provider's model
// list says, where it says it (OpenRouter).
func effortsFor(kind, id string, reasons bool) (efforts []ai.Effort, def string) {
	switch {
	case kind == "openai" && openaiReasoning(id), kind == "openrouter" && reasons:
		return []ai.Effort{low, medium, high}, "medium"
	case kind == "anthropic" && strings.HasPrefix(id, "claude-") && !claudeNoThinking.MatchString(id):
		return []ai.Effort{{Effort: "none", Description: "Answer without extended thinking"}, low, medium, high}, "none"
	case kind == "google" && strings.HasPrefix(id, "gemini-3"):
		return []ai.Effort{low, high}, "high"
	case kind == "google" && strings.HasPrefix(id, "gemini-2.5"):
		return []ai.Effort{low, medium, high}, "medium"
	}
	return []ai.Effort{}, ""
}

// openaiReasoning: o-series, GPT-5 (but not its chat variant) and codex models.
func openaiReasoning(id string) bool {
	return regexp.MustCompile(`^o\d`).MatchString(id) || strings.HasPrefix(id, "gpt-5") && !strings.HasPrefix(id, "gpt-5-chat") || strings.HasPrefix(id, "codex-")
}

// listed is one model from a provider's list.
type listed struct {
	id, name string
	reasons  bool // the provider says it takes a reasoning setting
}

// Not chat models, in OpenAI's list (it returns every model the key can use).
var openaiNonChat = regexp.MustCompile(`audio|realtime|tts|transcribe|whisper|dall-e|image|embedding|moderation|search|davinci|babbage|codex-mini|computer-use`)

// ListModels asks the provider which models the key can use, plus the ids the
// user typed in. Slugs are prefixed with the config's id.
func ListModels(ctx context.Context, hc *http.Client, c Config) ([]ai.Model, error) {
	k, _ := kind(c.Kind)
	var ids []listed
	if k.listsModels {
		var err error
		if ids, err = fetchModels(ctx, hc, c, k); err != nil && len(c.Models) == 0 {
			return nil, err
		}
	}
	for _, m := range c.Models {
		ids = append(ids, listed{id: m, name: m})
	}
	seen := map[string]bool{}
	out := []ai.Model{}
	for _, m := range ids {
		if seen[m.id] {
			continue
		}
		seen[m.id] = true
		efforts, def := effortsFor(c.Kind, m.id, m.reasons)
		out = append(out, ai.Model{Slug: c.ID + ":" + m.id, DisplayName: m.name, Efforts: efforts, DefaultEffort: def})
	}
	if out == nil {
		out = []ai.Model{}
	}
	return out, nil
}

func fetchModels(ctx context.Context, hc *http.Client, c Config, k Kind) ([]listed, error) {
	u := strings.TrimRight(c.base(), "/") + k.modelsPath
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case "anthropic":
		req.Header.Set("x-api-key", c.APIKey)
		req.Header.Set("anthropic-version", "2023-06-01")
	case "google":
		req.Header.Set("x-goog-api-key", c.APIKey)
	default:
		if c.APIKey != "" {
			req.Header.Set("Authorization", "Bearer "+c.APIKey)
		}
	}
	resp, err := hc.Do(req)
	if errors.Is(err, netguard.ErrPrivate) {
		return nil, &ai.Error{Kind: ai.ErrOther, Message: k.Name + ": " + netguard.ErrPrivate.Error()}
	}
	if err != nil {
		return nil, fmt.Errorf("%s is unreachable: %w", k.Name, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, &ai.Error{Kind: ai.ErrInvalidKey, Message: k.Name + " rejected the API key", Status: resp.StatusCode}
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s answered HTTP %d listing models", k.Name, resp.StatusCode)
	}
	var out []listed
	switch c.Kind {
	case "google":
		var r struct {
			Models []struct {
				Name        string   `json:"name"`
				DisplayName string   `json:"displayName"`
				Methods     []string `json:"supportedGenerationMethods"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			if slices.Contains(m.Methods, "generateContent") {
				out = append(out, listed{id: strings.TrimPrefix(m.Name, "models/"), name: m.DisplayName})
			}
		}
	case "ollama":
		var r struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Models {
			out = append(out, listed{id: m.Name, name: m.Name})
		}
	default: // OpenAI-style {"data":[{"id","name"|"display_name"}]}
		var r struct {
			Data []struct {
				ID          string   `json:"id"`
				Name        string   `json:"name"`
				DisplayName string   `json:"display_name"`
				Params      []string `json:"supported_parameters"` // OpenRouter
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &r); err != nil {
			return nil, err
		}
		for _, m := range r.Data {
			if c.Kind == "openai" && (openaiNonChat.MatchString(m.ID) || !(strings.HasPrefix(m.ID, "gpt") || strings.HasPrefix(m.ID, "chatgpt") || regexp.MustCompile(`^o\d`).MatchString(m.ID))) {
				continue
			}
			name := m.ID
			if m.DisplayName != "" {
				name = m.DisplayName
			} else if m.Name != "" {
				name = m.Name
			}
			out = append(out, listed{id: m.ID, name: name, reasons: slices.Contains(m.Params, "reasoning")})
		}
	}
	slices.SortStableFunc(out, func(a, b listed) int { return strings.Compare(a.name, b.name) })
	return out, nil
}
