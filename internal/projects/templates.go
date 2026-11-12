package projects

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/ponytojas/Refract/internal/access"
)

//go:embed templates/*.tex
var starterFiles embed.FS

type Template struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Builtin bool   `json:"builtin"`
}

var starters = []Template{
	{ID: "article", Name: "Article", Builtin: true},
	{ID: "thesis", Name: "Thesis", Builtin: true},
	{ID: "beamer", Name: "Presentation", Builtin: true},
	{ID: "letter", Name: "Letter", Builtin: true},
	{ID: "cv", Name: "CV", Builtin: true},
}

// Templates lists built-ins and only the signed-in user's saved templates.
func (s *Service) Templates(ctx context.Context) ([]Template, error) {
	u, err := access.UserFrom(ctx)
	if err != nil {
		return nil, err
	}
	out := append([]Template{}, starters...)
	rows, err := s.DB.QueryContext(ctx, `SELECT id, name FROM projects WHERE owner = ? AND is_template = 1 ORDER BY name, id`, u.ID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var template Template
		if err := rows.Scan(&template.ID, &template.Name); err != nil {
			return nil, err
		}
		out = append(out, template)
	}
	return out, rows.Err()
}

// CreateFromTemplate produces a normal project with its own files and fresh
// history. Source IDs arrive in a request body, so ownership is checked here
// as well as on the project-scoped save/delete routes.
func (s *Service) CreateFromTemplate(ctx context.Context, name, templateID string) (*Project, error) {
	if _, err := access.UserFrom(ctx); err != nil {
		return nil, err
	}
	for _, template := range starters {
		if template.ID != templateID {
			continue
		}
		body, err := starterFiles.ReadFile("templates/" + template.ID + ".tex")
		if err != nil {
			return nil, err
		}
		res, err := s.create(ctx, name, func(dir string) ([]string, error) {
			return extract(dir, func(yield func(string, io.Reader) error) error {
				return yield("main.tex", strings.NewReader(string(body)))
			}, s.Limits)
		}, "create from template")
		if err != nil {
			return nil, err
		}
		return res.Project, nil
	}
	if err := (&access.Checker{DB: s.DB}).Require(ctx, templateID, access.Owner); err != nil {
		return nil, err
	}
	source, err := s.Get(ctx, templateID)
	if err != nil {
		return nil, err
	}
	if !source.IsTemplate {
		return nil, ErrNotFound
	}
	return s.copyProject(ctx, source, name, false)
}

// SaveTemplate takes a private copy of the project's current source files,
// without its chats, remotes, credentials, build output or git history.
func (s *Service) SaveTemplate(ctx context.Context, id, name string) (*Template, error) {
	if err := (&access.Checker{DB: s.DB}).Require(ctx, id, access.Owner); err != nil {
		return nil, err
	}
	source, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	p, err := s.copyProject(ctx, source, name, true)
	if err != nil {
		return nil, err
	}
	return &Template{ID: p.ID, Name: p.Name}, nil
}

func (s *Service) copyProject(ctx context.Context, source *Project, name string, template bool) (*Project, error) {
	res, err := s.createWith(ctx, name, func(dir string) ([]string, error) {
		return extract(dir, projectSource(ctx, s.Path(source.ID)), s.Limits)
	}, "copy project source", creation{template: template, mainFile: &source.MainFile})
	if err != nil {
		return nil, err
	}
	return res.Project, nil
}

func projectSource(ctx context.Context, dir string) Source {
	return func(yield func(string, io.Reader) error) error {
		root, err := os.OpenRoot(dir)
		if err != nil {
			return err
		}
		defer root.Close()
		return walkFiles(dir, func(rel string, _ fs.FileInfo) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			f, err := root.Open(rel)
			if err != nil {
				return err
			}
			defer f.Close()
			info, err := f.Stat()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return ErrUnsafePath
			}
			return yield(rel, f)
		})
	}
}
