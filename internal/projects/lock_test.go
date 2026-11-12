package projects

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// Commits on the same project wait for each other instead of failing on
// .git/index.lock.
func TestConcurrentCheckpoints(t *testing.T) {
	s, id := newTurnProject(t)
	var wg sync.WaitGroup
	errs := make(chan error, 8*5)
	for n := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 5 {
				os.WriteFile(filepath.Join(s.Path(id), fmt.Sprintf("f%d.tex", n)), []byte(fmt.Sprintf("%d\n", i)), 0o644)
				if _, err := s.Checkpoint(ownerCtx, id, "c"); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if out, err := git(ownerCtx, s.Path(id), "status", "--porcelain"); err != nil || out != "" {
		t.Fatalf("working copy after checkpoints: %q %v", out, err)
	}
}

// Auto-push after every commit doesn't collide with the next commit.
func TestAutoPushDoesNotCollide(t *testing.T) {
	s, id, _ := remoteProject(t)
	errs := make(chan error, 5)
	s.OnCommit = func(ctx context.Context, id string) { errs <- s.Push(ctx, id) }
	commits := 0 // a push's own checkpoint may take the next edit first
	for i := range 5 {
		os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte(fmt.Sprintf("v%d\n", i)), 0o644)
		committed, err := s.Checkpoint(ownerCtx, id, "c")
		if err != nil {
			t.Fatal(err)
		}
		if committed {
			commits++
		}
	}
	for range commits {
		select {
		case err := <-errs:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("auto-push did not finish")
		}
	}
}

// An assistant turn runs from start to accept while the user's copy keeps
// being committed (compiles, auto-save checkpoints).
func TestTurnWhileCheckpointing(t *testing.T) {
	s, id := newTurnProject(t)
	errs := make(chan error, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 10 {
			os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte(fmt.Sprintf("v%d\n", i)), 0o644)
			if _, err := s.Checkpoint(ownerCtx, id, "c"); err != nil {
				errs <- err
			}
		}
	}()
	base := aiTurn(t, s, id, "t1")
	if _, conflicts, err := s.AcceptTurn(ownerCtx, id, "t1", base, nil, "ai: work"); err != nil || conflicts != nil {
		t.Errorf("accept: %v %v", conflicts, err)
	}
	if err := s.EndTurn(ownerCtx, id, "t1"); err != nil {
		t.Error(err)
	}
	<-done
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}
