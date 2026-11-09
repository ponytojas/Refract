package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/assistant"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/mode"
	"github.com/ponytojas/Refract/internal/providers"
	"github.com/ponytojas/Refract/internal/quick"
	"github.com/ponytojas/Refract/internal/siwc"
)

type api struct {
	base  *siwc.Session // DB, key, OAuth and allowlist shared by every user's session
	mode  mode.Mode     // REFRACT_MODE: whether the ChatGPT plan is offered
	model string        // default model (REFRACT_MODEL_AGENT)
	quick string        // default model for quick actions (REFRACT_MODEL_QUICK)

	canSignOut bool // REFRACT_AUTH=password: Refract keeps the session

	providers *providers.Store // users' API-key providers
	outbound  *http.Client     // for those providers (see providers.HTTPClient)

	users sync.Map // user id → *userAI
}

// userAI is one user's ChatGPT session and AI, kept for the life of the
// server: the session serializes that user's token refreshes, the ChatGPT
// provider remembers their usage-limit state, the router their model lists.
type userAI struct {
	session *siwc.Session
	router  *providers.Router // its ChatGPT is nil where the plan isn't offered
}

func (a *api) forUser(ctx context.Context) *userAI {
	u, _ := auth.UserFrom(ctx)
	if v, ok := a.users.Load(u.ID); ok {
		return v.(*userAI)
	}
	s := &siwc.Session{DB: a.base.DB, Key: a.base.Key, OAuth: a.base.OAuth, AllowedSubject: a.base.AllowedSubject, Owner: u.ID}
	router := &providers.Router{HTTP: a.outbound, Configs: func(ctx context.Context) ([]providers.Config, error) {
		return a.providers.List(ctx, u.ID)
	}}
	if a.mode.ChatGPT() { // never in saas, even with credentials stored earlier
		router.ChatGPT = &ai.SIWC{Tokens: s, BaseURL: siwc.Resource, HTTP: &http.Client{}}
	}
	v, _ := a.users.LoadOrStore(u.ID, &userAI{session: s, router: router})
	return v.(*userAI)
}

// providerFor is the request user's AI: their ChatGPT plan or API providers,
// by model.
func (a *api) providerFor(ctx context.Context) ai.Provider { return a.forUser(ctx).router }

func (a *api) routes(rt *router) {
	rt.user("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		u, _ := auth.UserFrom(r.Context())
		writeJSON(w, map[string]any{"id": u.ID, "email": u.Email, "can_sign_out": a.canSignOut, "mode": a.mode})
	})
	rt.user("GET /api/chatgpt/status", a.status)
	if a.mode.ChatGPT() { // elsewhere these fall through to a 404
		rt.user("POST /api/chatgpt/credentials", a.importCredentials)
		rt.user("POST /api/chatgpt/signout", a.signOut)
	}
	rt.user("POST /api/chatgpt/welcome-seen", a.welcomeSeen)
	rt.user("GET /api/models", a.models)
	rt.user("GET /api/providers", a.listProviders)
	rt.user("PUT /api/providers/{id}", a.saveProvider)
	rt.user("DELETE /api/providers/{id}", a.deleteProvider)
	rt.user("POST /api/providers/{id}/test", a.testProvider)
	rt.user("POST /api/quick", a.quickAction)
}

func (a *api) status(w http.ResponseWriter, r *http.Request) {
	u := a.forUser(r.Context())
	seen, err := u.session.WelcomeSeen(r.Context())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	cs, err := a.providers.List(r.Context(), requestUser(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	st := u.router.Status(r.Context())
	writeJSON(w, map[string]any{
		"status":        st,
		"ai_ready":      st.SignedIn && st.PlanGranted || len(cs) > 0,
		"welcome_seen":  seen,
		"manage_usage":  siwc.ManageUsage,
		"default_model": a.model,
	})
}

func (a *api) importCredentials(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if _, err := a.forUser(r.Context()).session.Import(r.Context(), raw); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	slog.Info("chatgpt credentials imported")
	a.status(w, r)
}

func (a *api) signOut(w http.ResponseWriter, r *http.Request) {
	if err := a.forUser(r.Context()).session.SignOut(r.Context()); err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	a.status(w, r)
}

func (a *api) welcomeSeen(w http.ResponseWriter, r *http.Request) {
	if err := a.forUser(r.Context()).session.MarkWelcomeSeen(r.Context()); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) models(w http.ResponseWriter, r *http.Request) {
	ms, err := a.forUser(r.Context()).router.Models(r.Context())
	if err != nil {
		var e *ai.Error
		if errors.As(err, &e) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]any{"error": e})
			return
		}
		httpError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, ms)
}

// choose resolves a model pick against the user's models: the UI's pick, else
// the configured model, else the first listed. Unknown efforts fall back to
// the model's default.
func (a *api) choose(ctx context.Context, want assistant.Choice, configured string) (assistant.Choice, error) {
	u := a.forUser(ctx)
	ms, err := u.router.Models(ctx)
	if err != nil {
		return assistant.Choice{}, err
	}
	slug := cmp.Or(want.Model, configured)
	m, effort, ok := ai.Pick(ms, slug, want.Effort)
	if !ok && want.Model == "" {
		m, effort, ok = ai.Pick(ms, "", "")
		if st := u.router.Status(ctx); slug != "" && st.SignedIn && st.PlanGranted {
			return assistant.Choice{Model: slug}, nil // configured, possibly unlisted on the plan: trust it
		}
	}
	switch {
	case ok:
		return assistant.Choice{Model: m.Slug, Effort: effort}, nil
	case len(ms) == 0:
		msg := "no models available: add an API provider in Settings"
		if a.mode.ChatGPT() {
			msg = "no models available: sign in with ChatGPT or add an API provider in Settings"
		}
		return assistant.Choice{}, &ai.Error{Kind: ai.ErrNotSignedIn, Message: msg}
	default:
		return assistant.Choice{}, fmt.Errorf("model %q is not available", want.Model)
	}
}

// quickAction runs one quick action on a selection and returns the
// replacement text (or, for "explain", the explanation).
func (a *api) quickAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		quick.Request
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if err := in.Validate(); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	// Quick actions should feel instant: light reasoning whatever the composer uses.
	c, err := a.choose(r.Context(), assistant.Choice{Model: in.Model, Effort: "low"}, a.quick)
	if err == nil {
		var text string
		if text, err = quick.Run(r.Context(), a.providerFor(r.Context()), c.Model, c.Effort, in.Request); err == nil {
			writeJSON(w, map[string]string{"text": text})
			return
		}
	}
	if r.Context().Err() != nil {
		return // the user cancelled
	}
	var ae *ai.Error
	if !errors.As(err, &ae) {
		ae = &ai.Error{Kind: ai.ErrOther, Message: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	json.NewEncoder(w).Encode(map[string]any{"error": ae})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, err error, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"kind": "other", "message": err.Error()}})
}
