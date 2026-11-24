package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseOutbound(t *testing.T) {
	for in, want := range map[string]bool{"": false, "any": false, "public": true} {
		if got, err := parseOutbound(in); err != nil || got != want {
			t.Errorf("parseOutbound(%q) = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"other", "Public", "private"} {
		if _, err := parseOutbound(in); err == nil {
			t.Errorf("parseOutbound(%q) accepted", in)
		}
	}
}

func TestGitAtLeast237(t *testing.T) {
	for in, want := range map[string]bool{
		"git version 2.37.0":                 true,
		"git version 2.36.9":                 false,
		"git version 2.50.1 (Apple Git-155)": true,
		"git version 3.0.0":                  true,
		"git version 1.9.5":                  false,
		"nonsense":                           false,
	} {
		if got := gitAtLeast237(in); got != want {
			t.Errorf("gitAtLeast237(%q) = %v", in, got)
		}
	}
}

// An unsupported git must stop startup before refract.db (or the data folder)
// is created.
func TestOutboundPublicChecksGitBeforeOpeningDatabase(t *testing.T) {
	stub := filepath.Join(t.TempDir(), "git")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho 'git version 2.36.9'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REFRACT_GIT_BIN", stub)
	t.Setenv("REFRACT_OUTBOUND", "public")
	dataDir := filepath.Join(t.TempDir(), "data")
	err := run(context.Background(), runOptions{dataDir: dataDir, authKind: "none"})
	if err == nil || !strings.Contains(err.Error(), "git 2.37") {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "refract.db")); !os.IsNotExist(err) {
		t.Fatalf("refract.db exists after a refused start: %v", err)
	}
}
