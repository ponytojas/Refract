package accountexport

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyExclusivePreservesExistingFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "staged.zip")
	if err := os.WriteFile(source, []byte("completed archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "account.zip")
	if err := copyExclusive(source, path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyExclusive(source, path); !errors.Is(err, os.ErrExist) {
		t.Errorf("second copy error = %v, want an existing-file error", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "completed archive" {
		t.Errorf("destination = %q (%v)", data, err)
	}
}
