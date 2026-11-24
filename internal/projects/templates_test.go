package projects

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/auth"
)

func TestSavedTemplatesArePrivateIndependentCopies(t *testing.T) {
	s := newService(t)
	p, err := s.Create(ownerCtx, "Original")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"paper/selected.tex": `\documentclass{article}\begin{document}Selected.\end{document}`,
		"other.tex":          `\documentclass{article}\begin{document}Other.\end{document}`,
		"assets/figure.dat":  "\x00\x01binary",
		"REFRACT.md":         "Use British spelling.",
	}
	for path, content := range files {
		if err := s.WriteFile(ownerCtx, p.ID, path, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetMainFile(ownerCtx, p.ID, "paper/selected.tex"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Checkpoint(ownerCtx, p.ID, "Original history"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(s.Path(p.ID), ".refract", "build"), 0o700); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(s.Path(p.ID), ".refract", "build", "main.pdf"), []byte("old build"), 0o600)
	outside := filepath.Join(t.TempDir(), "private")
	os.WriteFile(outside, []byte("outside"), 0o600)
	if err := os.Symlink(outside, filepath.Join(s.Path(p.ID), "outside.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES ('source-chat', ?, 'Private chat', 0, 0)`, p.ID); err != nil {
		t.Fatal(err)
	}

	saved, err := s.SaveTemplate(ownerCtx, p.ID, "My template")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Builtin {
		t.Fatal("saved template marked as built-in")
	}
	list, err := s.List(ownerCtx)
	if err != nil || len(list) != 1 {
		t.Fatalf("normal project list includes the template: %v %v", list, err)
	}
	if err := s.WriteFile(ownerCtx, p.ID, "REFRACT.md", strings.NewReader("Changed original.")); err != nil {
		t.Fatal(err)
	}
	copy, err := s.CreateFromTemplate(ownerCtx, "Copy", saved.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if copy.IsTemplate || copy.ID == p.ID || copy.ID == saved.ID || copy.MainFile != "paper/selected.tex" {
		t.Fatalf("metadata: %+v", copy)
	}
	for path, content := range files {
		b, _, err := s.ReadFile(ownerCtx, copy.ID, path)
		if err != nil || string(b) != content {
			t.Fatalf("%s: %q %v", path, b, err)
		}
	}
	for _, path := range []string{".refract/build/main.pdf", "outside.txt"} {
		if _, err := os.Stat(filepath.Join(s.Path(copy.ID), path)); !os.IsNotExist(err) {
			t.Fatalf("copied private/build file %s: %v", path, err)
		}
	}
	history, err := s.History(ownerCtx, copy.ID, 10)
	if err != nil || len(history) != 1 {
		t.Fatalf("inherited history: %v %v", history, err)
	}
	var chats int
	if err := s.DB.QueryRow(`SELECT count(*) FROM chats WHERE project_id IN (?, ?)`, saved.ID, copy.ID).Scan(&chats); err != nil || chats != 0 {
		t.Fatalf("inherited chats: %d %v", chats, err)
	}
	if _, err := s.Remote(ownerCtx, copy.ID); !errors.Is(err, ErrNoRemote) {
		t.Fatalf("inherited remote: %v", err)
	}
	if err := s.WriteFile(ownerCtx, copy.ID, "REFRACT.md", strings.NewReader("Changed copy.")); err != nil {
		t.Fatal(err)
	}
	if b, _, _ := s.ReadFile(ownerCtx, saved.ID, "REFRACT.md"); string(b) != files["REFRACT.md"] {
		t.Fatal("editing the copy changed the template")
	}
	if err := s.Trash(ownerCtx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if b, _, err := s.ReadFile(ownerCtx, copy.ID, "paper/selected.tex"); err != nil || string(b) != files["paper/selected.tex"] {
		t.Fatal("deleting the template changed a copy")
	}
}

func TestConcurrentTemplateCopiesAreIndependent(t *testing.T) {
	s := newService(t)
	p, err := s.CreateFromTemplate(ownerCtx, "Source", "article", "")
	if err != nil {
		t.Fatal(err)
	}
	base, err := s.SaveTemplate(ownerCtx, p.ID, "Base")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	ids, errs := make(chan string, 8), make(chan error, 8)
	for i := range 8 {
		wg.Go(func() {
			if i%2 == 0 {
				copy, err := s.CreateFromTemplate(ownerCtx, fmt.Sprint("Copy ", i), base.ID, "")
				if err != nil {
					errs <- err
					return
				}
				ids <- copy.ID
			} else {
				saved, err := s.SaveTemplate(ownerCtx, p.ID, fmt.Sprint("Saved ", i))
				if err != nil {
					errs <- err
					return
				}
				ids <- saved.ID
			}
		})
	}
	wg.Wait()
	close(errs)
	close(ids)
	for err := range errs {
		t.Error(err)
	}
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] || id == base.ID || id == p.ID {
			t.Fatalf("reused an id: %s", id)
		}
		seen[id] = true
		if b, _, err := s.ReadFile(ownerCtx, id, "main.tex"); err != nil || !strings.Contains(string(b), "Article title") {
			t.Fatalf("incomplete copy: %q %v", b, err)
		}
	}
	if len(seen) != 8 {
		t.Fatalf("only %d copies succeeded", len(seen))
	}
	list, err := s.List(ownerCtx)
	if err != nil || len(list) != 5 {
		t.Fatalf("project list: %d %v", len(list), err)
	}
	templates, err := s.Templates(ownerCtx)
	if err != nil || len(templates) != 10 {
		t.Fatalf("template list: %d %v", len(templates), err)
	}
}

func TestTemplateRegistrationFailureAndCancellationLeaveNothing(t *testing.T) {
	s := newService(t)
	p, err := s.CreateFromTemplate(ownerCtx, "Source", "article", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DB.Exec(`CREATE TRIGGER fail_template BEFORE INSERT ON projects WHEN NEW.is_template = 1 BEGIN SELECT RAISE(ABORT, 'test registration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveTemplate(ownerCtx, p.ID, "Failure"); err == nil {
		t.Fatal("registration failure was ignored")
	}
	ctx, cancel := context.WithCancel(ownerCtx)
	cancel()
	if _, err := s.SaveTemplate(ctx, p.ID, "Cancelled"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != p.ID {
		t.Fatalf("left files: %v %v", entries, err)
	}
	var count int
	if err := s.DB.QueryRow(`SELECT count(*) FROM projects`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("left records: %d %v", count, err)
	}
	if _, err := s.DB.Exec(`DROP TRIGGER fail_template`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveTemplate(ownerCtx, p.ID, "Recovered"); err != nil {
		t.Fatalf("subsequent save: %v", err)
	}
}

func TestTemplateSourceRejectsASymlinkPlantedDuringCopy(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	for _, path := range []string{filepath.Join(dir, "a.tex"), filepath.Join(dir, "z.tex"), outside} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var files []string
	err := projectSource(context.Background(), dir)(func(name string, _ io.Reader) error {
		files = append(files, name)
		if name == "a.tex" {
			if err := os.Remove(filepath.Join(dir, "z.tex")); err != nil {
				return err
			}
			return os.Symlink(outside, filepath.Join(dir, "z.tex"))
		}
		return nil
	})
	if err == nil || len(files) != 1 || files[0] != "a.tex" {
		t.Fatalf("followed a planted symlink: %v %v", files, err)
	}
}

func TestTemplatesRequireTheirOwner(t *testing.T) {
	s := newService(t)
	p, err := s.CreateFromTemplate(ownerCtx, "Article", "article", "")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveTemplate(ownerCtx, p.ID, "Private")
	if err != nil {
		t.Fatal(err)
	}
	other := auth.WithUser(context.Background(), auth.User{ID: "other"})
	for _, ctx := range []context.Context{other, context.Background()} {
		if _, err := s.SaveTemplate(ctx, p.ID, "Stolen"); !errors.Is(err, access.ErrNotFound) {
			t.Fatalf("saved another user's project: %v", err)
		}
		if _, err := s.CreateFromTemplate(ctx, "Stolen", saved.ID, ""); err == nil {
			t.Fatal("used another user's template")
		}
	}
	list, err := s.Templates(other)
	if err != nil || len(list) != 5 {
		t.Fatalf("template list leaks private entries: %v %v", list, err)
	}
	for _, item := range list {
		if !item.Builtin {
			t.Fatal("another user's template was listed")
		}
	}
	if _, err := s.CreateFromTemplate(context.Background(), "Anonymous", "article", ""); !errors.Is(err, access.ErrNoUser) {
		t.Fatalf("anonymous built-in creation: %v", err)
	}
	if _, err := s.CreateFromTemplate(ownerCtx, "Normal", p.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("normal project used as a template: %v", err)
	}
}

func TestTemplateCopyLimitsLeaveNoPartialProject(t *testing.T) {
	s := newService(t)
	p, err := s.CreateFromTemplate(ownerCtx, "Article", "article", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFile(ownerCtx, p.ID, "extra.txt", strings.NewReader("Extra")); err != nil {
		t.Fatal(err)
	}
	for _, limits := range []Limits{{MaxBytes: 1, MaxFiles: 5}, {MaxBytes: DefaultLimits.MaxBytes, MaxFiles: 1}} {
		s.Limits = limits
		if _, err := s.SaveTemplate(ownerCtx, p.ID, "Too large"); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("copy ignored limits: %v", err)
		}
		entries, err := os.ReadDir(s.Dir)
		if err != nil || len(entries) != 1 {
			t.Fatalf("partial project left behind: %v %v", entries, err)
		}
		list, _ := s.Templates(ownerCtx)
		if len(list) != 5 {
			t.Fatal("failed copy registered a template")
		}
	}
}

func TestTemplateTrashAndRestore(t *testing.T) {
	s := newService(t)
	p, err := s.CreateFromTemplate(ownerCtx, "Article", "article", "")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveTemplate(ownerCtx, p.ID, "Starter")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Trash(ownerCtx, p.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(ownerCtx); err != nil || len(list) != 0 {
		t.Fatalf("live list includes trash or templates: %+v, %v", list, err)
	}
	if err := s.Trash(ownerCtx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := s.Templates(ownerCtx); err != nil || len(list) != len(starters) {
		t.Fatalf("template list includes trash: %+v, %v", list, err)
	}
	if _, err := s.CreateFromTemplate(ownerCtx, "Copy", saved.ID, ""); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("copy trashed template: %v", err)
	}
	trash, err := s.ListTrash(ownerCtx)
	if err != nil || len(trash) != 2 {
		t.Fatalf("trash: %+v, %v", trash, err)
	}
	var found bool
	for _, p := range trash {
		if p.ID == saved.ID {
			found = p.IsTemplate
		}
	}
	if !found {
		t.Fatal("trashed template lost its type")
	}
	if err := s.Restore(ownerCtx, saved.ID); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(ownerCtx); err != nil || len(list) != 0 {
		t.Fatalf("restored template appears as a project: %+v, %v", list, err)
	}
	if _, err := s.CreateFromTemplate(ownerCtx, "Copy", saved.ID, ""); err != nil {
		t.Fatalf("copy restored template: %v", err)
	}
}

func TestBuiltinTemplateLanguagesAndSections(t *testing.T) {
	s := newService(t)
	for _, template := range starters {
		for _, language := range []string{"en", "es"} {
			t.Run(template.ID+"/"+language, func(t *testing.T) {
				p, err := s.CreateFromTemplate(ownerCtx, "Localized starter", template.ID, language)
				if err != nil {
					t.Fatal(err)
				}
				main, _, err := s.ReadFile(ownerCtx, p.ID, "main.tex")
				if err != nil {
					t.Fatal(err)
				}
				text := string(main)
				directory := "sections"
				if language == "es" {
					directory = "secciones"
				}
				if _, err := os.Stat(filepath.Join(s.Path(p.ID), directory)); err != nil {
					t.Fatalf("missing localized folder: %v", err)
				}
				if language == "es" {
					if _, err := os.Stat(filepath.Join(s.Path(p.ID), "sections")); !os.IsNotExist(err) {
						t.Fatal("Spanish template contains English folder")
					}
				}
				if !strings.Contains(text, `\input{`+directory+`/`) {
					t.Fatal("main file does not assemble section files")
				}
				want, unwanted := "Your name", "Tu nombre"
				if language == "es" {
					want, unwanted = unwanted, want
				}
				if !strings.Contains(text, want) || strings.Contains(text, unwanted) {
					t.Fatal("metadata has wrong language")
				}
				// Every input must exist in the new project and contain editable content.
				for _, part := range strings.Split(text, `\input{`)[1:] {
					path, _, ok := strings.Cut(part, "}")
					if !ok {
						t.Fatal("unclosed input")
					}
					body, _, err := s.ReadFile(ownerCtx, p.ID, path+".tex")
					if err != nil || len(body) == 0 {
						t.Fatalf("missing section %s: %v", path, err)
					}
				}
				if template.ID == "article" || template.ID == "thesis" {
					methods := "sections/methods.tex"
					want := `\section{Methods}`
					if template.ID == "thesis" {
						want = `\chapter{Research design and methods}`
					}
					if language == "es" {
						methods = "secciones/metodos.tex"
						want = `\section{Métodos}`
						if template.ID == "thesis" {
							want = `\chapter{Diseño de la investigación y métodos}`
						}
					}
					body, _, err := s.ReadFile(ownerCtx, p.ID, methods)
					if err != nil || !strings.Contains(string(body), want) {
						t.Fatalf("methods language: %v %s", err, body)
					}
				}
			})
		}
	}
}

func TestTemplateLanguageValidationAndSavedContent(t *testing.T) {
	s := newService(t)
	for _, language := range []string{"fr", "ES", "../en", "en/../../es"} {
		if _, err := s.CreateFromTemplate(ownerCtx, "Invalid", "article", language); !errors.Is(err, ErrTemplateLanguage) {
			t.Fatalf("language %q: %v", language, err)
		}
	}
	if list, err := s.List(ownerCtx); err != nil || len(list) != 0 {
		t.Fatalf("invalid language created a project: %v %v", list, err)
	}
	p, err := s.CreateFromTemplate(ownerCtx, "Spanish", "article", "es")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := s.SaveTemplate(ownerCtx, p.ID, "Spanish starter")
	if err != nil {
		t.Fatal(err)
	}
	copy, err := s.CreateFromTemplate(ownerCtx, "Still Spanish", saved.ID, "en")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"main.tex", "secciones/resumen.tex", "secciones/metodos.tex"} {
		original, _, err := s.ReadFile(ownerCtx, p.ID, path)
		if err != nil {
			t.Fatal(err)
		}
		copied, _, err := s.ReadFile(ownerCtx, copy.ID, path)
		if err != nil || string(original) != string(copied) {
			t.Fatalf("saved content changed: %s %v", path, err)
		}
	}
}
