package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/ponytojas/Refract/internal/access"
	"log/slog"
	"net/http"

	"github.com/ponytojas/Refract/internal/agent"
	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/assistant"
	"github.com/ponytojas/Refract/internal/projects"
)

type assistantAPI struct {
	svc *assistant.Service
}

func (a *assistantAPI) routes(rt *router) {
	rt.project("GET /api/projects/{id}/chats", access.Owner, a.chats)
	rt.project("POST /api/projects/{id}/chats", access.Owner, a.createChat)
	rt.chat("GET /api/chats/{chat}/turns", access.Owner, a.turns)
	rt.chat("POST /api/chats/{chat}/turns", access.Owner, a.ask)
	rt.turn("GET /api/turns/{turn}/diff", access.Owner, a.diff)
	rt.turn("POST /api/turns/{turn}/accept", access.Owner, a.accept)
	rt.turn("POST /api/turns/{turn}/reject", access.Owner, a.reject)
}

func (a *assistantAPI) chats(w http.ResponseWriter, r *http.Request) {
	cs, err := a.svc.Chats(r.Context(), r.PathValue("id"))
	if err != nil {
		assistantError(w, err)
		return
	}
	writeJSON(w, cs)
}

func (a *assistantAPI) createChat(w http.ResponseWriter, r *http.Request) {
	c, err := a.svc.CreateChat(r.Context(), r.PathValue("id"))
	if err != nil {
		assistantError(w, err)
		return
	}
	writeJSON(w, c)
}

func (a *assistantAPI) turns(w http.ResponseWriter, r *http.Request) {
	ts, err := a.svc.Turns(r.Context(), r.PathValue("chat"))
	if err != nil {
		assistantError(w, err)
		return
	}
	writeJSON(w, ts)
}

// ask streams a turn as server-sent events: text and activity while it runs,
// then the finished turn. Closing the connection stops the turn.
func (a *assistantAPI) ask(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Message string            `json:"message"`
		Context assistant.Context `json:"context"`
		Choice  assistant.Choice  `json:"choice"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	rc := http.NewResponseController(w)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		rc.Flush()
	}
	turn, err := a.svc.Ask(r.Context(), r.PathValue("chat"), in.Message, in.Context, in.Choice, func(e agent.Event) { send(e) })
	if err != nil {
		var ae *ai.Error
		if !errors.As(err, &ae) {
			ae = &ai.Error{Kind: ai.ErrOther, Message: err.Error()}
		}
		if !errors.Is(err, assistant.ErrBusy) {
			slog.Error("assistant turn", "err", err)
		}
		send(map[string]any{"type": "error", "error": ae})
		return
	}
	send(map[string]any{"type": "turn", "turn": turn})
}

func (a *assistantAPI) diff(w http.ResponseWriter, r *http.Request) {
	d, err := a.svc.Diff(r.Context(), r.PathValue("turn"))
	if err != nil {
		assistantError(w, err)
		return
	}
	writeJSON(w, d)
}

func (a *assistantAPI) accept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Files []string `json:"files"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&in); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	turn, conflicts, err := a.svc.Accept(r.Context(), r.PathValue("turn"), in.Files)
	if err != nil {
		assistantError(w, err)
		return
	}
	writeJSON(w, map[string]any{"turn": turn, "conflicts": conflicts})
}

func (a *assistantAPI) reject(w http.ResponseWriter, r *http.Request) {
	turn, err := a.svc.Reject(r.Context(), r.PathValue("turn"))
	if err != nil {
		assistantError(w, err)
		return
	}
	writeJSON(w, map[string]any{"turn": turn})
}

func assistantError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, assistant.ErrNotFound), errors.Is(err, projects.ErrNotFound):
		httpError(w, err, http.StatusNotFound)
	case errors.Is(err, assistant.ErrState), errors.Is(err, assistant.ErrBusy), errors.Is(err, projects.ErrConflict):
		httpError(w, err, http.StatusConflict)
	default:
		projectError(w, err)
	}
}
