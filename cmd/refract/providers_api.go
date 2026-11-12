package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/providers"
)

// providerView is a configured provider as the browser sees it: never the key.
type providerView struct {
	providers.Config
	HasKey bool `json:"has_key"`
}

func (a *api) listProviders(w http.ResponseWriter, r *http.Request) {
	cs, err := a.providers.List(r.Context(), requestUser(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	out := make([]providerView, len(cs))
	for i, c := range cs {
		out[i] = providerView{Config: c, HasKey: c.APIKey != ""}
		out[i].APIKey = ""
	}
	writeJSON(w, map[string]any{"kinds": providers.KindsFor(a.mode.PrivateNetwork()), "configured": out})
}

func (a *api) saveProvider(w http.ResponseWriter, r *http.Request) {
	var c providers.Config
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&c); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	c.ID = r.PathValue("id")
	if !a.mode.PrivateNetwork() {
		err := c.Validate()
		if err == nil {
			err = c.CheckPublic()
		}
		if err != nil {
			httpError(w, err, http.StatusBadRequest)
			return
		}
	}
	if err := a.providers.Save(r.Context(), requestUser(r.Context()), c); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	a.listProviders(w, r)
}

func (a *api) deleteProvider(w http.ResponseWriter, r *http.Request) {
	if err := a.providers.Delete(r.Context(), requestUser(r.Context()), r.PathValue("id")); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	a.listProviders(w, r)
}

// testProvider lists the provider's models: it proves the key and the URL.
func (a *api) testProvider(w http.ResponseWriter, r *http.Request) {
	cs, err := a.providers.List(r.Context(), requestUser(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	for _, c := range cs {
		if c.ID != r.PathValue("id") {
			continue
		}
		ms, err := providers.ListModels(r.Context(), a.outbound, c)
		if err != nil {
			var ae *ai.Error
			if !errors.As(err, &ae) {
				ae = &ai.Error{Kind: ai.ErrUnavailable, Message: err.Error()}
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadGateway)
			json.NewEncoder(w).Encode(map[string]any{"error": ae})
			return
		}
		writeJSON(w, map[string]int{"models": len(ms)})
		return
	}
	http.NotFound(w, r)
}
