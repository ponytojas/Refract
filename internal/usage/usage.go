// Package usage records what each model call consumed: who made it, which
// model answered and how many tokens it took. It keeps no text. Credits,
// spending limits and cost reports are built on these rows.
package usage

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/auth"
)

// Entry is one model call.
type Entry struct {
	UserID string
	Kind   string // agent, quick or cite
	Model  string
	Usage  ai.Usage
}

// Recorder writes and sums the ai_usage table.
type Recorder struct {
	DB  *sql.DB
	Now func() time.Time // nil means time.Now
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Record stores one call. A call nobody is signed in for is billed to no one,
// so an empty UserID writes nothing.
func (r *Recorder) Record(ctx context.Context, e Entry) error {
	if e.UserID == "" {
		return nil
	}
	u := e.Usage
	_, err := r.DB.ExecContext(ctx, `INSERT INTO ai_usage
		(at, user_id, kind, model, input_tokens, cached_tokens, output_tokens, reasoning_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.now().Unix(), e.UserID, e.Kind, e.Model, u.InputTokens, u.CachedTokens, u.OutputTokens, u.ReasoningTokens)
	return err
}

// Totals sums a user's calls to one model. CachedTokens is part of
// InputTokens and ReasoningTokens part of OutputTokens, not additions to them.
type Totals struct {
	Calls           int `json:"calls"`
	InputTokens     int `json:"input_tokens"`
	CachedTokens    int `json:"cached_tokens"`
	OutputTokens    int `json:"output_tokens"`
	ReasoningTokens int `json:"reasoning_tokens"`
}

// Since returns a user's totals per model for the calls made at or after since.
func (r *Recorder) Since(ctx context.Context, userID string, since time.Time) (map[string]Totals, error) {
	rows, err := r.DB.QueryContext(ctx, `SELECT model, count(*), sum(input_tokens), sum(cached_tokens), sum(output_tokens), sum(reasoning_tokens)
		FROM ai_usage WHERE user_id = ? AND at >= ? GROUP BY model`, userID, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Totals{}
	for rows.Next() {
		var model string
		var t Totals
		if err := rows.Scan(&model, &t.Calls, &t.InputTokens, &t.CachedTokens, &t.OutputTokens, &t.ReasoningTokens); err != nil {
			return nil, err
		}
		out[model] = t
	}
	return out, rows.Err()
}

// Meter is an ai.Provider that records the usage of every call Inner
// completes, for the user in the call's context.
type Meter struct {
	Inner ai.Provider
	Rec   *Recorder
	Kind  string
}

// Stream passes Inner's events through unchanged and records the call's usage
// when it completes. A failure to record is logged and never fails the call.
func (m *Meter) Stream(ctx context.Context, req ai.Request) (<-chan ai.Event, error) {
	in, err := m.Inner.Stream(ctx, req)
	if err != nil {
		return nil, err
	}
	u, _ := auth.UserFrom(ctx)
	out := make(chan ai.Event)
	go func() {
		defer close(out)
		for ev := range in {
			if ev.Type == ai.EventCompleted && ev.Usage != nil {
				// The call happened even if the user has since stopped the turn.
				e := Entry{UserID: u.ID, Kind: m.Kind, Model: req.Model, Usage: *ev.Usage}
				if err := m.Rec.Record(context.WithoutCancel(ctx), e); err != nil {
					slog.Error("recording model usage", "err", err)
				}
			}
			select {
			case out <- ev:
			case <-ctx.Done():
				for range in { // let Inner finish: it stops on ctx too
				}
				return
			}
		}
	}()
	return out, nil
}

// Status is Inner's.
func (m *Meter) Status(ctx context.Context) ai.Status { return m.Inner.Status(ctx) }
