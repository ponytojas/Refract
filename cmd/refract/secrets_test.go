package main

import (
	"encoding/base64"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/users"
)

func TestSecretFromEnv(t *testing.T) {
	const name = "REFRACT_SECRET_TEST"
	const fileVar = name + "_FILE"
	dir := t.TempDir()
	path := filepath.Join(dir, "credential")
	if err := os.WriteFile(path, []byte("  file-value\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing-credential")

	for _, tt := range []struct {
		name, value, path, want, wantError string
		missing                            bool
	}{
		{name: "unset"},
		{name: "file only", path: path, want: "file-value"},
		{name: "variable only", value: " env-value ", want: " env-value "},
		{name: "both", value: "env-value", path: path, wantError: name + " and " + fileVar},
		{name: "missing file", path: missing, wantError: fileVar, missing: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(name, tt.value)
			t.Setenv(fileVar, tt.path)
			got, err := secretFromEnv(name)
			if tt.wantError == "" {
				if err != nil || got != tt.want {
					t.Fatalf("unexpected secret or error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("expected error naming %s", tt.wantError)
			}
			if got != "" || strings.Contains(err.Error(), "file-value") ||
				strings.Contains(err.Error(), "env-value") || strings.Contains(err.Error(), dir) {
				t.Fatal("the failed read exposed the value or file path")
			}
			if tt.missing && !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("expected a file-not-found error")
			}
		})
	}
}

func TestUsersListWithSecretKeyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "refract.key")
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REFRACT_DATA_DIR", dir)
	t.Setenv("REFRACT_SECRET_KEY", "")
	t.Setenv("REFRACT_SECRET_KEY_FILE", path)
	if err := runUsers([]string{"list"}, io.Discard); err != nil {
		t.Fatal(err)
	}
}

func TestOIDCSecretFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oidc")
	if err := os.WriteFile(path, []byte("oidc-test-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("REFRACT_OIDC_CLIENT_SECRET", "")
	t.Setenv("REFRACT_OIDC_CLIENT_SECRET_FILE", path)
	t.Setenv("REFRACT_OIDC_ISSUER", "https://id.example.com")
	t.Setenv("REFRACT_OIDC_CLIENT_ID", "refract-test")
	t.Setenv("REFRACT_PUBLIC_URL", "https://refract.example.com")
	_, login, err := authMiddleware(&users.Service{}, "builtin")
	if err != nil {
		t.Fatal(err)
	}
	if login.oidc == nil || login.oidc.ClientSecret != "oidc-test-value" {
		t.Fatal("the OIDC client did not receive the file value")
	}
}

func TestSMTPURLFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "smtp")
	t.Setenv("REFRACT_SMTP_URL", "")
	t.Setenv("REFRACT_SMTP_URL_FILE", path)
	t.Setenv("REFRACT_MAIL_FROM", "refract@example.com")
	if err := os.WriteFile(path, []byte("smtp://user:test-password@smtp.example.com\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if smtp, err := mailFromEnv(); err != nil || smtp == nil {
		t.Fatalf("expected SMTP configuration from the file: %v", err)
	}

	if err := os.WriteFile(path, []byte("smtp:#private-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := mailFromEnv(); err == nil || !strings.Contains(err.Error(), "REFRACT_SMTP_URL") ||
		strings.Contains(err.Error(), "private-value") {
		t.Fatal("invalid SMTP URL should be reported without exposing the file value")
	}
}
