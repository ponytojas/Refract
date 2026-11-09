package main

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/ponytojas/Refract/internal/access"
	"net/http"
	"os"
	"strings"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/assistant"
	"github.com/ponytojas/Refract/internal/cite"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/refs"
)

type papersAPI struct {
	papers   *cite.Service
	projects *projects.Service
	provider func(context.Context) ai.Provider                                 // the request user's
	choose   func(context.Context, assistant.Choice) (assistant.Choice, error) // the model for suggestions and placement
}

func (a *papersAPI) routes(rt *router) {
	rt.user("GET /api/papers/search", a.search)
	rt.project("POST /api/projects/{id}/papers/suggest", access.Owner, a.suggest)
	rt.project("POST /api/projects/{id}/cite", access.Owner, a.cite)
}

const maxPassage = 8000

// forUser is the citation service with the request user's AI provider.
func (a *papersAPI) forUser(ctx context.Context) *cite.Service {
	s := *a.papers
	s.Provider = a.provider(ctx)
	return &s
}

// search takes free text, or a DOI / arXiv id / URL for one paper.
func (a *papersAPI) search(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" || len(q) > 300 {
		httpError(w, errors.New("query must be 1 to 300 characters"), http.StatusBadRequest)
		return
	}
	cs, err := a.papers.Search(r.Context(), q)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	writeJSON(w, cs)
}

// suggest finds papers that support a passage, each with the reason.
func (a *papersAPI) suggest(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text  string `json:"text"`
		Model string `json:"model"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(in.Text) == "" || len(in.Text) > maxPassage {
		httpError(w, errors.New("select a passage (up to 8,000 characters)"), http.StatusBadRequest)
		return
	}
	if _, err := a.projects.Get(r.Context(), r.PathValue("id")); err != nil {
		projectError(w, err)
		return
	}
	c, err := a.choose(r.Context(), assistant.Choice{Model: in.Model, Effort: "low"})
	if err != nil {
		aiError(w, r, err)
		return
	}
	cs, err := a.forUser(r.Context()).Suggest(r.Context(), c.Model, c.Effort, in.Text)
	if err != nil {
		aiError(w, r, err)
		return
	}
	writeJSON(w, cs)
}

// cite adds a paper to the project's bibliography. Only the id comes from the
// client: the entry is built from the source's metadata. With place, the
// citation is also positioned in the passage (the client shows it as a diff).
func (a *papersAPI) cite(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Place *struct {
			Selection string `json:"selection"`
			Before    string `json:"before"`
			After     string `json:"after"`
		} `json:"place"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	id, ok := cite.ParseID(strings.TrimPrefix(strings.TrimPrefix(in.ID, "doi:"), "arxiv:"))
	if !ok {
		httpError(w, errors.New("not a DOI or an arXiv id"), http.StatusBadRequest)
		return
	}
	if in.Place != nil && (len(in.Place.Selection) > maxPassage || len(in.Place.Before)+len(in.Place.After) > 2*maxPassage) {
		httpError(w, errors.New("passage too long"), http.StatusBadRequest)
		return
	}
	p, err := a.projects.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		projectError(w, err)
		return
	}
	ref, cand, err := a.papers.Resolve(r.Context(), id)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	root, err := os.OpenRoot(a.projects.Path(p.ID))
	if err != nil {
		projectError(w, err)
		return
	}
	defer root.Close()
	got, err := refs.Cite(root, p.MainFile, ref)
	if err != nil {
		projectError(w, err)
		return
	}
	out := struct {
		refs.Citation
		Text   string `json:"text,omitempty"`
		Placed bool   `json:"placed"`
	}{Citation: got}
	if in.Place != nil && strings.TrimSpace(in.Place.Selection) != "" {
		c, err := a.choose(r.Context(), assistant.Choice{Model: in.Model, Effort: "low"})
		if err != nil {
			aiError(w, r, err)
			return
		}
		out.Text, out.Placed = a.forUser(r.Context()).Place(r.Context(), c.Model, c.Effort, cand, got.Key, in.Place.Selection, in.Place.Before, in.Place.After)
	}
	writeJSON(w, out)
}

// aiError reports a model error with its kind (usage limit, sign-in, …) for the UI.
func aiError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	var ae *ai.Error
	if !errors.As(err, &ae) {
		ae = &ai.Error{Kind: ai.ErrOther, Message: err.Error()}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadGateway)
	json.NewEncoder(w).Encode(map[string]any{"error": ae})
}
