// Package accountexport builds the account archive used by the web and CLI.
package accountexport

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

func writeJSON(zw *zip.Writer, name string, value any) error {
	dst, err := zw.Create(name)
	if err != nil {
		return err
	}
	return json.NewEncoder(dst).Encode(value)
}

func Write(ctx context.Context, w io.Writer, ps *projects.Service, owner, tmp string) error {
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
		if err := writeJSON(zw, entry.name, entry.value); err != nil {
			return err
		}
	}
	chats := &assistant.Service{DB: ps.DB}
	type projectError struct {
		ID      string `json:"project_id"`
		Message string `json:"message"`
	}
	var omitted []projectError
	for _, item := range items {
		sourceErr, err := appendProject(ctx, zw, ps, chats, owner, item.ID, tmp)
		if err != nil {
			return err
		}
		if sourceErr != nil {
			slog.Warn("project omitted from account export", "project", item.ID, "err", sourceErr)
			omitted = append(omitted, projectError{ID: item.ID, Message: "Could not export this project."})
		}
	}
	if len(omitted) > 0 {
		if err := writeJSON(zw, "errors.json", omitted); err != nil {
			return err
		}
	}
	return zw.Close()
}

// Stage a project separately so a failed source read leaves no partial entry in
// the account archive. Copy the compressed entries without recompressing them.
func appendProject(ctx context.Context, zw *zip.Writer, ps *projects.Service, chats *assistant.Service, owner, id, tmp string) (sourceErr, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(tmp, "refract-project-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(f.Name())
	staged := zip.NewWriter(f)
	sourceErr = ps.WriteAccountProject(ctx, staged, owner, id, tmp)
	if sourceErr == nil {
		var dst io.Writer
		dst, sourceErr = staged.Create("projects/" + id + "/chats.json")
		if sourceErr == nil {
			sourceErr = chats.WriteAccountChats(ctx, dst, owner, id)
		}
	}
	if err := errors.Join(staged.Close(), f.Close()); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if sourceErr != nil {
		var pathErr *os.PathError
		if errors.As(sourceErr, &pathErr) && pathErr.Path == f.Name() {
			return nil, sourceErr
		}
		return sourceErr, nil
	}
	zr, err := zip.OpenReader(f.Name())
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	for _, entry := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := zw.Copy(entry); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

// WriteFile publishes the complete archive with owner-only permissions and
// refuses to replace an existing destination.
func WriteFile(ctx context.Context, path string, ps *projects.Service, owner, tmp string) error {
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(tmp, "refract-cli-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	f, err := os.CreateTemp(filepath.Dir(path), ".refract-export-*.zip")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	err = Write(ctx, f, ps, owner, dir)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	err = os.Link(f.Name(), path)
	if err == nil || errors.Is(err, os.ErrExist) {
		return err
	}
	// Some destination filesystems do not support hard links. O_EXCL gives
	// the same protection against overwriting a file created in the meantime.
	return copyExclusive(f.Name(), path)
}

func copyExclusive(source, path string) error {
	src, err := os.Open(source)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	if err := errors.Join(err, dst.Close()); err != nil {
		return errors.Join(err, os.Remove(path))
	}
	return nil
}
