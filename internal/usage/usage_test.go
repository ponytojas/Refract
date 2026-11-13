package usage

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/store"
)

// replay is a provider that answers every call with the same events.
type replay []ai.Event

func (r replay) Status(context.Context) ai.Status { return ai.Status{} }
func (r replay) Stream(context.Context, ai.Request) (<-chan ai.Event, error) {
	ch := make(chan ai.Event, len(r))
	for _, ev := range r {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// endless sends text until its context is done, then closes.
type endless struct{}

func (endless) Status(context.Context) ai.Status { return ai.Status{} }
func (endless) Stream(ctx context.Context, _ ai.Request) (<-chan ai.Event, error) {
	ch := make(chan ai.Event)
	go func() {
		defer close(ch)
		for {
			select {
			case ch <- ai.Event{Type: ai.EventText, Text: "x"}:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

var clock = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

func newRecorder(t *testing.T) *Recorder {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return &Recorder{DB: db, Now: func() time.Time { return clock }}
}

func ana() context.Context { return auth.WithUser(context.Background(), auth.User{ID: "ana"}) }

var used = ai.Usage{InputTokens: 100, OutputTokens: 40, TotalTokens: 140, CachedTokens: 60, ReasoningTokens: 25}

type row struct {
	At                               int64
	User, Kind, Model                string
	Input, Cached, Output, Reasoning int
}

func rows(t *testing.T, db *sql.DB) []row {
	t.Helper()
	rs, err := db.Query(`SELECT at, user_id, kind, model, input_tokens, cached_tokens, output_tokens, reasoning_tokens FROM ai_usage ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.At, &r.User, &r.Kind, &r.Model, &r.Input, &r.Cached, &r.Output, &r.Reasoning); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func drain(ch <-chan ai.Event) []ai.Event {
	var evs []ai.Event
	for ev := range ch {
		evs = append(evs, ev)
	}
	return evs
}

func TestMeterRecordsCompletedCalls(t *testing.T) {
	rec := newRecorder(t)
	inner := replay{{Type: ai.EventText, Text: "hi"}, {Type: ai.EventCompleted, Usage: &used}}
	ch, err := (&Meter{Inner: inner, Rec: rec, Kind: "agent"}).Stream(ana(), ai.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(ch); !reflect.DeepEqual(got, []ai.Event(inner)) {
		t.Fatalf("events = %+v, want %+v", got, inner)
	}
	want := []row{{clock.Unix(), "ana", "agent", "m1", 100, 60, 40, 25}}
	if got := rows(t, rec.DB); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v, want %+v", got, want)
	}
}

func TestMeterSkipsFailedCalls(t *testing.T) {
	rec := newRecorder(t)
	inner := replay{{Type: ai.EventText, Text: "hi"}, {Type: ai.EventError, Err: &ai.Error{Message: "no"}}}
	ch, err := (&Meter{Inner: inner, Rec: rec, Kind: "agent"}).Stream(ana(), ai.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(ch); len(got) != 2 {
		t.Fatalf("events = %+v", got)
	}
	if got := rows(t, rec.DB); len(got) != 0 {
		t.Fatalf("rows = %+v, want none", got)
	}
}

func TestMeterWithoutUser(t *testing.T) {
	rec := newRecorder(t)
	inner := replay{{Type: ai.EventCompleted, Usage: &used}}
	ch, err := (&Meter{Inner: inner, Rec: rec, Kind: "cite"}).Stream(context.Background(), ai.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	if got := drain(ch); len(got) != 1 {
		t.Fatalf("events = %+v", got)
	}
	if got := rows(t, rec.DB); len(got) != 0 {
		t.Fatalf("rows = %+v, want none", got)
	}
}

func TestMeterStopsWithItsCaller(t *testing.T) {
	ctx, cancel := context.WithCancel(ana())
	ch, err := (&Meter{Inner: endless{}, Rec: newRecorder(t), Kind: "agent"}).Stream(ctx, ai.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	<-ch
	cancel()
	done := make(chan struct{})
	go func() { drain(ch); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the meter's channel didn't close after cancel")
	}
}

// A call that completed is recorded even if the caller stopped meanwhile.
func TestMeterRecordsAfterCancel(t *testing.T) {
	rec := newRecorder(t)
	ctx, cancel := context.WithCancel(ana())
	cancel()
	ch, err := (&Meter{Inner: replay{{Type: ai.EventCompleted, Usage: &used}}, Rec: rec, Kind: "agent"}).Stream(ctx, ai.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	drain(ch)
	if got := rows(t, rec.DB); len(got) != 1 {
		t.Fatalf("rows = %+v, want one", got)
	}
}

func TestSince(t *testing.T) {
	rec := newRecorder(t)
	ctx := context.Background()
	day1, day2 := clock, clock.Add(24*time.Hour)
	add := func(at time.Time, user, model string, in, out int) {
		rec.Now = func() time.Time { return at }
		if err := rec.Record(ctx, Entry{UserID: user, Kind: "agent", Model: model, Usage: ai.Usage{InputTokens: in, OutputTokens: out, CachedTokens: 1, ReasoningTokens: 2}}); err != nil {
			t.Fatal(err)
		}
	}
	add(day1, "ana", "m1", 1000, 1000) // before the window
	add(day2, "ana", "m1", 10, 20)
	add(day2, "ana", "m1", 30, 40)
	add(day2, "ana", "m2", 5, 6)
	add(day2, "bob", "m1", 7, 8) // another user
	got, err := rec.Since(ctx, "ana", day2)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Totals{
		"m1": {Calls: 2, InputTokens: 40, CachedTokens: 2, OutputTokens: 60, ReasoningTokens: 4},
		"m2": {Calls: 1, InputTokens: 5, CachedTokens: 1, OutputTokens: 6, ReasoningTokens: 2},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Since = %+v, want %+v", got, want)
	}
}

// Quick actions and citations call ai.Complete, which stops reading at the
// completion event.
func TestCompleteThroughMeter(t *testing.T) {
	rec := newRecorder(t)
	inner := replay{{Type: ai.EventText, Text: "hello"}, {Type: ai.EventCompleted, Usage: &used}}
	text, err := ai.Complete(ana(), &Meter{Inner: inner, Rec: rec, Kind: "quick"}, ai.Request{Model: "m1"})
	if err != nil || text != "hello" {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	if got := rows(t, rec.DB); len(got) != 1 || got[0].Kind != "quick" {
		t.Fatalf("rows = %+v, want one quick row", got)
	}
}

// A call that completes while the caller is gone (cancelled before reading the
// text that came first) is still recorded.
func TestMeterRecordsCompletionAfterUnreadEvents(t *testing.T) {
	rec := newRecorder(t)
	ctx, cancel := context.WithCancel(ana())
	cancel()
	inner := replay{{Type: ai.EventText, Text: "hi"}, {Type: ai.EventCompleted, Usage: &used}}
	ch, err := (&Meter{Inner: inner, Rec: rec, Kind: "agent"}).Stream(ctx, ai.Request{Model: "m1"})
	if err != nil {
		t.Fatal(err)
	}
	// Don't read yet: with nobody reading, the meter can only take the cancel path.
	deadline := time.Now().Add(2 * time.Second)
	for len(rows(t, rec.DB)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	drain(ch)
	if got := rows(t, rec.DB); len(got) != 1 {
		t.Fatalf("rows = %+v, want one", got)
	}
}
