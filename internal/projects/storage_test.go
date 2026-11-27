package projects

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStorageBytesDoesNotWaitForRepoLock(t *testing.T) {
	s := &Service{Dir: t.TempDir()}
	const id = "storage-fixture"
	if err := os.MkdirAll(s.Path(id), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Path(id), "main.tex"), []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	unlock := lockRepo(id)
	defer unlock()
	type result struct {
		bytes int64
		err   error
	}
	done := make(chan result, 1)
	go func() {
		n, err := s.StorageBytes(t.Context(), id)
		done <- result{n, err}
	}()
	select {
	case got := <-done:
		if got.err != nil || got.bytes != 6 {
			t.Errorf("storage = %d (%v), want 6 bytes", got.bytes, got.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("storage estimate waited for the repository lock")
	}
}
