package compile

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

func TestBuiltInTemplatesCompile(t *testing.T) {
	tc := tectonic(t)
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	ctx := auth.WithUser(context.Background(), auth.User{ID: "owner"})
	templates, err := s.Templates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(templates) != 5 {
		t.Fatalf("expected five built-ins, got %d", len(templates))
	}
	for _, template := range templates {
		t.Run(template.ID, func(t *testing.T) {
			p, err := s.CreateFromTemplate(ctx, template.Name, template.ID)
			if err != nil {
				t.Fatal(err)
			}
			result, err := tc.Compile(ctx, s.Path(p.ID), p.MainFile)
			if err != nil || !result.OK || result.PDF == "" {
				t.Fatalf("template compile: %+v %v", result, err)
			}
			// A successful compile can still hide clipped text or broken links.
			log, err := os.ReadFile(filepath.Join(s.Path(p.ID), BuildDir, "main.log"))
			if err != nil {
				t.Fatal(err)
			}
			for _, warning := range []string{"Overfull", "undefined references", "Citation `", "Reference `"} {
				if strings.Contains(string(log), warning) {
					t.Fatalf("template layout or reference warning %q", warning)
				}
			}
			if template.ID == "cv" || template.ID == "letter" {
				if pages := pdfPages(t, filepath.Join(s.Path(p.ID), result.PDF)); pages != 1 {
					t.Fatalf("%s should fit one page, got %d", template.ID, pages)
				}
			}
		})
	}
}

func TestSavedTemplateWithUnicodePathsAndAssetsCompiles(t *testing.T) {
	tc := tectonic(t)
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	ctx := auth.WithUser(context.Background(), auth.User{ID: "owner"})
	p, err := s.Create(ctx, "Unicode source")
	if err != nil {
		t.Fatal(err)
	}
	for path, source := range map[string]string{
		"documentos/mi artículo.tex": `\documentclass{article}\usepackage{graphicx}\begin{document}\input{parts/body}\includegraphics{figures/dot.png}\end{document}`,
		"documentos/parts/body.tex":  `Copied content with \textbf{formatting} and $x^2$.`,
	} {
		if err := s.WriteFile(ctx, p.ID, path, strings.NewReader(source)); err != nil {
			t.Fatal(err)
		}
	}
	var picture bytes.Buffer
	if err := png.Encode(&picture, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile(ctx, p.ID, "documentos/figures/dot.png", &picture); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMainFile(ctx, p.ID, "documentos/mi artículo.tex"); err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveTemplate(ctx, p.ID, "Artículo base")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.CreateFromTemplate(ctx, "Independent copy", saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if copy.MainFile != "documentos/mi artículo.tex" {
		t.Fatal("lost selected main file")
	}
	result, err := tc.Compile(ctx, s.Path(copy.ID), copy.MainFile)
	if err != nil || !result.OK || result.PDF == "" {
		t.Fatalf("custom template compile: %+v %v", result, err)
	}
}
