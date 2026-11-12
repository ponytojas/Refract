package assistant

import (
	"errors"
	"testing"

	"github.com/ponytojas/Refract/internal/agent"
	"github.com/ponytojas/Refract/internal/ai"
)

func TestReviewIsExclusive(t *testing.T) {
	s := &Service{}
	release, err := s.review("t")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.review("t"); !errors.Is(err, ErrReviewing) {
		t.Fatalf("second review of the same turn: %v", err)
	}
	other, err := s.review("u")
	if err != nil {
		t.Fatalf("another turn: %v", err)
	}
	other()
	release()
	again, err := s.review("t")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	again()
}

// A double click or a second tab can't accept and reject the same turn.
func TestSecondAcceptIsRefused(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
		done("Added related work."),
	}}
	s, ps, _, chat := setup(t, p)
	ctx := ownerCtx
	turn, err := s.Ask(ctx, chat, "add related work", Context{}, Choice{}, func(agent.Event) {})
	if err != nil || turn.Status != "review" {
		t.Fatal(turn, err)
	}

	release, err := s.review(turn.ID) // an accept in progress
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Accept(ctx, turn.ID, nil); !errors.Is(err, ErrReviewing) {
		t.Fatalf("accept during another: %v", err)
	}
	if _, err := s.Reject(ctx, turn.ID); !errors.Is(err, ErrReviewing) {
		t.Fatalf("reject during an accept: %v", err)
	}
	turns, _ := (&Service{DB: s.DB, Projects: ps}).Turns(ctx, chat)
	if len(turns) != 1 || turns[0].Status != "review" {
		t.Fatalf("turn changed while reserved: %+v", turns)
	}
	release()

	if accepted, _, err := s.Accept(ctx, turn.ID, nil); err != nil || accepted.Status != "accepted" {
		t.Fatalf("accept: %+v %v", accepted, err)
	}
	if _, err := s.Reject(ctx, turn.ID); !errors.Is(err, ErrState) {
		t.Fatalf("reject after accept: %v", err)
	}
}
