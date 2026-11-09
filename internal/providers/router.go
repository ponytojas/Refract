package providers

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/ai"
)

// Router is one user's AI: models prefixed with a configured provider's id
// ("anthropic:claude-…") go to that provider with the user's key, the rest to
// their ChatGPT plan. It implements ai.Provider, so nothing above it changes.
type Router struct {
	ChatGPT *ai.SIWC                                    // nil when the ChatGPT plan isn't offered (REFRACT_MODE=saas)
	Configs func(ctx context.Context) ([]Config, error) // the user's providers
	HTTP    *http.Client

	mu    sync.Mutex
	cache map[string]cached // config id → its models
}

type cached struct {
	models []ai.Model
	key    string // the config they were listed with
	at     time.Time
}

const modelsTTL = 10 * time.Minute

func (r *Router) Status(ctx context.Context) ai.Status {
	if r.ChatGPT == nil {
		return ai.Status{Provider: "chatgpt"} // same shape, never signed in
	}
	return r.ChatGPT.Status(ctx)
}

// chatgpt is the provider for a slug without a configured provider's prefix.
func (r *Router) chatgpt(slug string) (ai.Provider, string, error) {
	if r.ChatGPT == nil {
		return nil, "", &ai.Error{Kind: ai.ErrNotSignedIn, Message: fmt.Sprintf("model %q is not available: add an API provider in Settings", slug)}
	}
	return r.ChatGPT, slug, nil
}

// route finds the provider for a model slug and the model id it expects.
func (r *Router) route(ctx context.Context, slug string) (ai.Provider, string, error) {
	prefix, model, ok := strings.Cut(slug, ":")
	if !ok {
		return r.chatgpt(slug)
	}
	cs, err := r.Configs(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, c := range cs {
		if c.ID == prefix {
			return &Provider{Config: c, HTTP: r.HTTP}, model, nil
		}
	}
	return r.chatgpt(slug) // not a provider prefix (ChatGPT slugs have no ':')
}

func (r *Router) Stream(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	p, model, err := r.route(ctx, req.Model)
	if err != nil {
		return nil, err
	}
	req.Model = model
	return p.Stream(ctx, req)
}

// Models lists every model the user can pick: their ChatGPT plan's (when
// signed in), then each configured provider's. A provider that fails to list
// is left out rather than failing the whole list.
func (r *Router) Models(ctx context.Context) ([]ai.Model, error) {
	out := []ai.Model{}
	var firstErr error
	if st := r.Status(ctx); st.SignedIn && st.PlanGranted {
		ms, err := r.ChatGPT.Models(ctx)
		if err != nil {
			firstErr = err
		}
		for _, m := range ms {
			m.Provider, m.ProviderName = "chatgpt", "ChatGPT"
			out = append(out, m)
		}
	}
	cs, err := r.Configs(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range cs {
		ms, err := r.listCached(ctx, c)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		name := c.Name
		if name == "" {
			k, _ := kind(c.Kind)
			name = k.Name
		}
		for _, m := range ms {
			m.Provider, m.ProviderName = c.ID, name
			out = append(out, m)
		}
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func (r *Router) listCached(ctx context.Context, c Config) ([]ai.Model, error) {
	stamp := c.Kind + "|" + c.APIKey + "|" + c.BaseURL + "|" + strings.Join(c.Models, ",")
	r.mu.Lock()
	hit, ok := r.cache[c.ID]
	r.mu.Unlock()
	if ok && hit.key == stamp && time.Since(hit.at) < modelsTTL {
		return hit.models, nil
	}
	ms, err := ListModels(ctx, r.HTTP, c)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.cache == nil {
		r.cache = map[string]cached{}
	}
	r.cache[c.ID] = cached{models: ms, key: stamp, at: time.Now()}
	r.mu.Unlock()
	return ms, nil
}
