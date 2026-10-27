package compile

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/fonts"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

// TestFixtures compiles every private Prism export in fixtures/ (gitignored).
// fixtures/<name>.pdf, when present, is Prism's PDF: page counts must match.
// Original exports using named fonts are resolved just like imported projects.
func TestFixtures(t *testing.T) {
	zips, _ := filepath.Glob("../../fixtures/*.zip")
	if len(zips) == 0 {
		t.Skip("no fixtures/*.zip")
	}
	tc := tectonic(t)
	tc.Fonts = fonts.NewResolver(filepath.Join(t.TempDir(), "font-cache"))
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	svc := &projects.Service{DB: db, Dir: t.TempDir(), Limits: projects.DefaultLimits}

	for _, z := range zips {
		name := strings.TrimSuffix(filepath.Base(z), ".zip")
		t.Run(name, func(t *testing.T) {
			res, err := svc.ImportZip(context.Background(), name, z)
			if err != nil {
				t.Fatal(err)
			}
			if res.Project.MainFile == "" {
				t.Fatalf("main file not detected; candidates %v", res.Candidates)
			}
			out, err := tc.Compile(context.Background(), svc.Path(res.Project.ID), res.Project.MainFile)
			if err != nil {
				t.Fatal(err)
			}
			if out.PDF == "" {
				t.Fatalf("no PDF: %+v", out.Problems)
			}
			for _, p := range out.Problems {
				if p.Severity == "error" {
					t.Logf("document error (PDF still produced): %s:%d %s", p.File, p.Line, p.Message)
				}
			}
			if want := filepath.Join("../../fixtures", name+".pdf"); fileExists(want) {
				if got, exp := pdfPages(t, filepath.Join(svc.Path(res.Project.ID), out.PDF)), pdfPages(t, want); got != exp {
					t.Errorf("pages = %d, Prism's PDF has %d", got, exp)
				}
			}
		})
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
