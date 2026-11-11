package projects

import (
	"context"
	"sync"
	"testing"
	"time"
)

// Commits can still happen while shutdown waits (a request that outlived
// the drain): registering them must not race the wait.
func TestCommitDuringDrain(t *testing.T) {
	s := &Service{OnCommit: func(context.Context, string) {}}
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				s.committed(context.Background(), "p")
			}
		}()
	}
	for range 20 {
		s.Drain(time.Millisecond)
	}
	wg.Wait()
}

// After Drain, commits still land but start no auto-push.
func TestNoOnCommitAfterDrain(t *testing.T) {
	called := false
	s := &Service{OnCommit: func(context.Context, string) { called = true }}
	if !s.Drain(time.Second) {
		t.Fatal("Drain with nothing running timed out")
	}
	s.committed(context.Background(), "p")
	if !s.Drain(time.Second) || called {
		t.Errorf("OnCommit called after Drain: %v", called)
	}
}
