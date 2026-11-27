package main

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerRefusesRemovedModeBeforeListening(t *testing.T) {
	for _, value := range []string{"saas", "selfhost", "unknown"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("REFRACT_MODE", value)
			err := run(context.Background(), runOptions{
				listen: func() (net.Listener, error) {
					t.Fatal("server tried to open a listener")
					return nil, nil
				},
			})
			if err == nil {
				t.Fatal("startup accepted REFRACT_MODE")
			}
			for _, name := range []string{"REFRACT_MODE", "REFRACT_PROFILE=team", "REFRACT_OUTBOUND=public"} {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("startup error %q does not mention %s", err, name)
				}
			}
		})
	}
}

func TestLocalRefusesRemovedModeBeforeCreatingData(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "refract")
	err := startLocal(context.Background(), localOptions{
		getenv:     func(key string) string { return map[string]string{"REFRACT_MODE": "saas"}[key] },
		defaultDir: dataDir,
	})
	if err == nil || !strings.Contains(err.Error(), "REFRACT_MODE was removed") {
		t.Errorf("startup error = %v, want REFRACT_MODE removal", err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("data directory should not exist, stat error = %v", err)
	}
}
