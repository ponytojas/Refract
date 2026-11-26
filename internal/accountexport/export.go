// Package accountexport builds the account archive used by the web and CLI.
package accountexport

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/ponytojas/Refract/internal/assistant"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/providers"
	"github.com/ponytojas/Refract/internal/siwc"
	"github.com/ponytojas/Refract/internal/usage"
	"github.com/ponytojas/Refract/internal/users"
)

// Only explicitly selected settings are exported. Provider endpoints may
// contain secrets in their paths or queries, even without URL userinfo.
type providerSettings struct {
	ID     string   `json:"id"`
	Kind   string   `json:"kind"`
	Name   string   `json:"name"`
	Models []string `json:"models,omitempty"`
}

type settings struct {
	Version     int                `json:"archive_version"`
	Email       string             `json:"email"`
	Lang        string             `json:"email_language"`
	Verified    bool               `json:"email_verified"`
	WelcomeSeen bool               `json:"welcome_seen"`
	Providers   []providerSettings `json:"providers"`
}

func Write(ctx context.Context, w io.Writer, ps *projects.Service, owner string) error {
	acct, err := (&users.Service{DB: ps.DB}).AccountByID(ctx, owner)
	if err != nil {
		return err
	}
	configs, err := (&providers.Store{DB: ps.DB, Key: ps.Key}).List(ctx, owner)
	if err != nil {
		return fmt.Errorf("read provider settings: %w", err)
	}
	seen, err := (&siwc.Session{DB: ps.DB, Owner: owner}).WelcomeSeen(ctx)
	if err != nil {
		return err
	}
	cfg := settings{Version: 1, Email: acct.Email, Lang: acct.Lang, Verified: acct.Verified, WelcomeSeen: seen,
		Providers: make([]providerSettings, 0, len(configs))}
	for _, c := range configs {
		cfg.Providers = append(cfg.Providers, providerSettings{ID: c.ID, Kind: c.Kind, Name: c.Name, Models: c.Models})
	}
	items, err := ps.AccountProjects(ctx, owner)
	if err != nil {
		return err
	}
	totals, err := (&usage.Recorder{DB: ps.DB}).Since(ctx, owner, time.Time{})
	if err != nil {
		return err
	}
	zw := zip.NewWriter(w)
	for _, entry := range []struct {
		name  string
		value any
	}{{"settings.json", cfg}, {"projects.json", items}, {"usage.json", totals}} {
		dst, err := zw.Create(entry.name)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(dst).Encode(entry.value); err != nil {
			return err
		}
	}
	chats := &assistant.Service{DB: ps.DB}
	for _, item := range items {
		if err := ps.WriteAccountProject(ctx, zw, owner, item.ID); err != nil {
			return fmt.Errorf("export project %s: %w", item.ID, err)
		}
		dst, err := zw.Create("projects/" + item.ID + "/chats.json")
		if err != nil {
			return err
		}
		if err := chats.WriteAccountChats(ctx, dst, owner, item.ID); err != nil {
			return fmt.Errorf("export chats for %s: %w", item.ID, err)
		}
	}
	return zw.Close()
}

// WriteFile publishes the complete archive with owner-only permissions and
// refuses to replace an existing destination.
func WriteFile(ctx context.Context, path string, ps *projects.Service, owner string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".refract-export-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = Write(ctx, f, ps, owner)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Link(f.Name(), path)
}
