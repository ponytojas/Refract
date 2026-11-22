package compile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
	"github.com/ponytojas/Refract/internal/tools"
)

// TestSeedCoversBuiltInTemplates applies a real TeX seed to an empty cache and
// compiles every built-in template in English and Spanish through the Runner
// with it: nothing may be downloaded, and SyncTeX must map a source line to the
// PDF and back. It needs the seed built by scripts/build-tex-seed.sh:
//
//	REFRACT_TEST_TEX_SEED=<out>/tex-seed go test ./internal/compile -run SeedCovers
//
// (the folder with seed.json, or the one holding tex-seed/). Block the network
// around the run (scripts/smoke-offline.sh does) to prove it needs none.
func TestSeedCoversBuiltInTemplates(t *testing.T) {
	dir := os.Getenv("REFRACT_TEST_TEX_SEED")
	if dir == "" {
		t.Skip("REFRACT_TEST_TEX_SEED is not set")
	}
	if _, err := os.Stat(filepath.Join(dir, "seed.json")); err != nil {
		dir = filepath.Join(dir, "tex-seed")
	}
	seed, why := FindSeed(func(k string) string {
		if k == "REFRACT_TEX_SEED" {
			return dir
		}
		return ""
	})
	if seed == nil {
		t.Fatalf("seed not usable: %s", why)
	}
	if tools.Find("tectonic").Path == "" || tools.Find("synctex").Path == "" {
		t.Fatal("this test needs tectonic and synctex")
	}
	cache := filepath.Join(t.TempDir(), "tectonic-cache")
	if _, err := seed.Apply(cache); err != nil {
		t.Fatal(err)
	}
	tc := &Tectonic{Bundle: seed.BundleURL, CacheDir: cache, Seed: seed, Timeout: 90 * time.Second}
	runner := &Runner{Compiler: tc}

	tmp := t.TempDir()
	db, err := store.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &projects.Service{DB: db, Dir: filepath.Join(tmp, "projects"), Limits: projects.DefaultLimits}
	ctx := auth.WithUser(context.Background(), auth.User{ID: "owner"})
	templates, err := s.Templates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) == 0 {
		t.Fatal("no templates")
	}
	sync := &SyncTeX{}
	for _, language := range []string{"en", "es"} {
		for _, template := range templates {
			t.Run(template.ID+"/"+language, func(t *testing.T) {
				p, err := s.CreateFromTemplate(ctx, template.Name, template.ID, language)
				if err != nil {
					t.Fatal(err)
				}
				projectDir := s.Path(p.ID)
				res, err := runner.Compile(ctx, p.ID, projectDir, p.MainFile)
				if err != nil {
					t.Fatal(err)
				}
				if !res.OK || res.PDF == "" || res.Downloads.Fetched != 0 || res.Downloads.Offline || len(res.Downloads.Missing) != 0 {
					t.Fatalf("compile: ok=%v pdf=%q downloads=%+v problems=%+v", res.OK, res.PDF, res.Downloads, res.Problems)
				}
				syncRoundTrip(t, sync, projectDir, p.MainFile)
			})
		}
	}
}

// syncRoundTrip finds a source line that has a place in the PDF and checks that
// clicking there leads back to the same file (beamer maps a click to the frame, not the line).
func syncRoundTrip(t *testing.T, s *SyncTeX, projectDir, mainFile string) {
	t.Helper()
	ctx := context.Background()
	var files []string
	filepath.WalkDir(projectDir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".tex") && !strings.Contains(p, BuildDir) {
			rel, _ := filepath.Rel(projectDir, p)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	for _, file := range files {
		b, err := os.ReadFile(filepath.Join(projectDir, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		for i, text := range strings.Split(string(b), "\n") {
			text = strings.TrimSpace(text)
			if len(text) < 30 || strings.HasPrefix(text, "\\") || strings.HasPrefix(text, "%") {
				continue // a plain sentence
			}
			boxes, err := s.Forward(ctx, projectDir, mainFile, file, i+1, 0)
			if err != nil || len(boxes) == 0 || boxes[0].W <= 0 {
				continue
			}
			b := boxes[0]
			got, line, err := s.Inverse(ctx, projectDir, mainFile, b.Page, b.X+b.W/2, b.Y+b.H/2)
			if err != nil || got != file || line < 1 {
				t.Fatalf("SyncTeX %s:%d -> page %d -> %s:%d %v", file, i+1, b.Page, got, line, err)
			}
			return
		}
	}
	t.Fatalf("no source line of %v maps to the PDF", files)
}
