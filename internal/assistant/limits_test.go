package assistant

import (
	"context"
	"errors"
	"testing"

	"github.com/ponytojas/Refract/internal/agent"
	"github.com/ponytojas/Refract/internal/ai"
)

func turnLimits(l TurnLimits) func(context.Context, string) (TurnLimits, error) {
	return func(context.Context, string) (TurnLimits, error) { return l, nil }
}

func TestTooManyTurns(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{done("a"), done("b")}, block: make(chan struct{}), entered: make(chan struct{})}
	s, ps, _, chatA := setup(t, p)
	s.LimitsFor = turnLimits(TurnLimits{Running: 1})
	b, err := ps.Create(ownerCtx, "second")
	if err != nil {
		t.Fatal(err)
	}
	chatB, err := s.CreateChat(ownerCtx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error)
	go func() {
		_, err := s.Ask(ownerCtx, chatA, "slow", Context{}, Choice{}, func(agent.Event) {})
		result <- err
	}()
	<-p.entered
	if _, err := s.Ask(ownerCtx, chatB.ID, "second", Context{}, Choice{}, func(agent.Event) {}); !errors.Is(err, ErrTooManyTurns) {
		t.Fatalf("second project: %v", err)
	}
	close(p.block)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(ownerCtx, chatB.ID, "second", Context{}, Choice{}, func(agent.Event) {}); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestReviewPending(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
		done("changed"),
		done("again"),
	}}
	s, _, _, chat := setup(t, p)
	s.LimitsFor = turnLimits(TurnLimits{PendingReview: 1})
	turn, err := s.Ask(ownerCtx, chat, "add related work", Context{}, Choice{}, func(agent.Event) {})
	if err != nil || turn.Status != "review" {
		t.Fatal(turn, err)
	}
	if _, err := s.Ask(ownerCtx, chat, "more", Context{}, Choice{}, func(agent.Event) {}); !errors.Is(err, ErrReviewPending) {
		t.Fatalf("second ask: %v", err)
	}
	if _, err := s.Reject(ownerCtx, turn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ask(ownerCtx, chat, "more", Context{}, Choice{}, func(agent.Event) {}); err != nil {
		t.Fatalf("after reject: %v", err)
	}
}

func TestTurnCountIsReleasedOnFailure(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{{Type: ai.EventError, Err: &ai.Error{Kind: ai.ErrOther, Message: "boom"}}},
		done("ok"),
	}}
	s, _, _, chat := setup(t, p)
	s.LimitsFor = turnLimits(TurnLimits{Running: 1})
	s.Ask(ownerCtx, chat, "fails", Context{}, Choice{}, func(agent.Event) {})
	if _, err := s.Ask(ownerCtx, chat, "again", Context{}, Choice{}, func(agent.Event) {}); errors.Is(err, ErrTooManyTurns) {
		t.Fatal("turn count leaked")
	}
}
