package main

import (
	"os/exec"
	"strings"
	"testing"
)

func TestExcludeFromBackup(t *testing.T) {
	dir := t.TempDir()
	excludeFromBackup(dir)
	out, err := exec.Command("/usr/bin/tmutil", "isexcluded", dir).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "[Excluded]") {
		t.Fatalf("tmutil isexcluded: %q %v", out, err)
	}
}
