// Package agent runs the assistant's tool-calling loop over a turn's worktree.
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/cite"
	"github.com/ponytojas/Refract/internal/compile"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/refs"
)

const (
	maxReadLines   = 400
	maxSearchLines = 100
	maxListFiles   = 2000
)

// Tools executes tool calls inside one worktree. Paths are validated like the
// file API (no escaping the project, no .git or .refract).
type Tools struct {
	Dir      string
	MainFile string
	Compiler compile.Compiler
	Papers   *cite.Service // nil: no paper tools
}

func schema(props string, required ...string) json.RawMessage {
	req, _ := json.Marshal(required)
	if required == nil {
		req = []byte("[]")
	}
	return json.RawMessage(`{"type":"object","properties":{` + props + `},"required":` + string(req) + `,"additionalProperties":false}`)
}

// Definitions describes the tools to the model.
func (t *Tools) Definitions() []ai.Tool {
	fn := func(name, desc string, params json.RawMessage) ai.Tool {
		return ai.Tool{Type: "function", Name: name, Description: desc, Parameters: params}
	}
	defs := []ai.Tool{
		fn("list_files", "List every file in the project with its size in bytes.", schema(``)),
		fn("read_file", "Read a text file. Long files are returned in parts: use start_line and end_line (1-based, inclusive) to read more.",
			schema(`"path":{"type":"string"},"start_line":{"type":["integer","null"]},"end_line":{"type":["integer","null"]}`, "path", "start_line", "end_line")),
		fn("search", "Search the project for literal text (case-sensitive). Returns file:line:text matches. glob optionally limits the files, e.g. \"*.tex\".",
			schema(`"query":{"type":"string"},"glob":{"type":["string","null"]}`, "query", "glob")),
		fn("edit_file", "Replace old_text with new_text in a file. old_text must match exactly once, including whitespace; include surrounding lines to make it unique.",
			schema(`"path":{"type":"string"},"old_text":{"type":"string"},"new_text":{"type":"string"}`, "path", "old_text", "new_text")),
		fn("write_file", "Create a new file, or overwrite a file completely. Prefer edit_file for changes to existing files.",
			schema(`"path":{"type":"string"},"content":{"type":"string"}`, "path", "content")),
		fn("compile", "Compile the project's main file. Returns errors and warnings with file and line. Set include_log to also get Tectonic's output.",
			schema(`"include_log":{"type":["boolean","null"]}`, "include_log")),
	}
	if t.Papers != nil {
		defs = append(defs,
			fn("search_papers", "Search published papers (OpenAlex) and arXiv preprints. Returns id (doi:… or arxiv:…), title, authors, year, venue and the start of the abstract.",
				schema(`"query":{"type":"string"}`, "query")),
			fn("cite", "Add a paper to the project's bibliography (.bib), built from the publisher's or arXiv's metadata, and return its citation key. id is a DOI or an arXiv id (as returned by search_papers). Does nothing if it is already there.",
				schema(`"id":{"type":"string"}`, "id")),
		)
	}
	return defs
}

// Call runs one tool. It never fails the turn: problems are reported to the
// model as the output so it can correct itself. activity is a short line for the UI.
func (t *Tools) Call(ctx context.Context, name, args string) (output, activity string) {
	out, act, err := t.call(ctx, name, args)
	if err != nil {
		return "Error: " + err.Error(), act + " (failed)"
	}
	return out, act
}

func (t *Tools) call(ctx context.Context, name, args string) (string, string, error) {
	var a struct {
		Path       string  `json:"path"`
		StartLine  *int    `json:"start_line"`
		EndLine    *int    `json:"end_line"`
		Query      string  `json:"query"`
		Glob       *string `json:"glob"`
		OldText    string  `json:"old_text"`
		NewText    string  `json:"new_text"`
		Content    string  `json:"content"`
		IncludeLog *bool   `json:"include_log"`
		ID         string  `json:"id"`
	}
	if args != "" {
		if err := json.Unmarshal([]byte(args), &a); err != nil {
			return "", name, fmt.Errorf("invalid arguments: %v", err)
		}
	}
	switch name {
	case "list_files":
		out, err := t.listFiles()
		return out, "listed files", err
	case "read_file":
		out, err := t.readFile(a.Path, a.StartLine, a.EndLine)
		return out, "read " + a.Path, err
	case "search":
		out, err := t.search(ctx, a.Query, a.Glob)
		return out, fmt.Sprintf("searched %q", a.Query), err
	case "edit_file":
		return "Edited " + a.Path + ".", "edited " + a.Path, t.editFile(a.Path, a.OldText, a.NewText)
	case "write_file":
		return "Wrote " + a.Path + ".", "wrote " + a.Path, t.writeFile(a.Path, a.Content)
	case "compile":
		return t.compile(ctx, a.IncludeLog != nil && *a.IncludeLog)
	case "search_papers":
		if t.Papers != nil {
			out, err := t.searchPapers(ctx, a.Query)
			return out, fmt.Sprintf("searched papers for %q", a.Query), err
		}
	case "cite":
		if t.Papers != nil {
			out, err := t.cite(ctx, a.ID)
			return out, "cited " + a.ID, err
		}
	}
	return "", name, fmt.Errorf("unknown tool %q", name)
}

func (t *Tools) listFiles() (string, error) {
	var b strings.Builder
	n := 0
	err := filepath.WalkDir(t.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p != t.Dir && (d.Name() == ".git" || d.Name() == ".refract") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil // a worktree's .git is a file
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if n++; n > maxListFiles {
			return filepath.SkipAll
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(t.Dir, p)
		fmt.Fprintf(&b, "%s (%d bytes)\n", filepath.ToSlash(rel), info.Size())
		return nil
	})
	if n > maxListFiles {
		fmt.Fprintf(&b, "… (more than %d files, list truncated)\n", maxListFiles)
	}
	return b.String(), err
}

func (t *Tools) open(p string) (*os.Root, string, error) {
	clean, err := projects.CheckPath(p)
	if err != nil {
		return nil, "", err
	}
	root, err := os.OpenRoot(t.Dir)
	return root, clean, err
}

func (t *Tools) readFile(p string, start, end *int) (string, error) {
	root, p, err := t.open(p)
	if err != nil {
		return "", err
	}
	defer root.Close()
	b, err := root.ReadFile(p)
	if err != nil {
		return "", err
	}
	if bytes.IndexByte(b, 0) >= 0 || !utf8.Valid(b) {
		return "", errors.New("not a text file")
	}
	lines := strings.SplitAfter(string(b), "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	from, to := 1, len(lines)
	if start != nil {
		from = max(*start, 1)
	}
	if end != nil {
		to = min(*end, len(lines))
	}
	if to-from+1 > maxReadLines {
		to = from + maxReadLines - 1
	}
	if len(lines) == 0 {
		return "(empty file)", nil
	}
	if from > len(lines) || from > to {
		return "", fmt.Errorf("line range %d-%d is outside the file (%d lines)", from, to, len(lines))
	}
	head := ""
	if from != 1 || to != len(lines) {
		head = fmt.Sprintf("[lines %d-%d of %d]\n", from, to, len(lines))
	}
	return head + strings.Join(lines[from-1:to], ""), nil
}

func (t *Tools) search(ctx context.Context, query string, glob *string) (string, error) {
	if query == "" {
		return "", errors.New("empty query")
	}
	args := []string{"--fixed-strings", "--line-number", "--no-heading", "--color=never", "--max-columns=300", "--glob=!.refract", "--glob=!.git"}
	if glob != nil && *glob != "" {
		args = append(args, "--glob="+*glob)
	}
	cmd := exec.CommandContext(ctx, "rg", append(args, "--", query, ".")...)
	cmd.Dir = t.Dir
	out, err := cmd.Output()
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return "No matches.", nil
	}
	if err != nil {
		return "", fmt.Errorf("search failed: %v", err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(l, "./")
	}
	if len(lines) > maxSearchLines {
		lines = append(lines[:maxSearchLines], fmt.Sprintf("… (%d more matches; narrow the query or glob)", len(lines)-maxSearchLines))
	}
	return strings.Join(lines, "\n"), nil
}

func (t *Tools) editFile(p, old, new string) error {
	root, p, err := t.open(p)
	if err != nil {
		return err
	}
	defer root.Close()
	b, err := root.ReadFile(p)
	if err != nil {
		return err
	}
	if old == "" {
		return errors.New("old_text is empty; use write_file to create files")
	}
	switch n := strings.Count(string(b), old); n {
	case 0:
		return errors.New("old_text not found; read the file again and copy the text exactly")
	case 1:
	default:
		return fmt.Errorf("old_text matches %d times; include more surrounding text so it matches exactly once", n)
	}
	return root.WriteFile(p, []byte(strings.Replace(string(b), old, new, 1)), 0o644)
}

func (t *Tools) writeFile(p, content string) error {
	root, p, err := t.open(p)
	if err != nil {
		return err
	}
	defer root.Close()
	if d := path.Dir(p); d != "." {
		if err := root.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	return root.WriteFile(p, []byte(content), 0o644)
}

func (t *Tools) compile(ctx context.Context, includeLog bool) (string, string, error) {
	if t.MainFile == "" {
		return "", "compile", errors.New("the project has no main file set")
	}
	res, err := t.Compiler.Compile(compile.AsAssistant(ctx), t.Dir, t.MainFile)
	if errors.Is(err, compile.ErrBusy) || errors.Is(err, compile.ErrOwnBusy) { // a result the model can act on, not an error that ends the turn
		return "The server is busy compiling. Try the compile tool again in a moment.", "compile (server busy)", nil
	}
	if err != nil {
		return "", "compile", err
	}
	var errs []string
	warnings := map[string]int{}
	for _, p := range res.Problems {
		if p.Severity == "error" {
			errs = append(errs, fmt.Sprintf("%s:%d: %s", p.File, p.Line, p.Message))
		} else {
			warnings[p.Kind]++
		}
	}
	var b strings.Builder
	switch {
	case res.TimedOut:
		b.WriteString("Compilation timed out (possible infinite loop).\n")
	case len(errs) == 0:
		b.WriteString("Compiled without errors.\n")
	default:
		fmt.Fprintf(&b, "%d error(s):\n", len(errs))
		for _, e := range errs[:min(len(errs), 20)] {
			b.WriteString(e + "\n")
		}
	}
	kinds := make([]string, 0, len(warnings))
	for k := range warnings {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	for _, k := range kinds {
		fmt.Fprintf(&b, "%d %s warning(s)\n", warnings[k], k)
	}
	if includeLog {
		b.WriteString("\nOutput:\n" + res.Output[:min(len(res.Output), 8000)])
	}
	act := "compiled: ok"
	if len(errs) > 0 {
		act = fmt.Sprintf("compiled: %d error(s)", len(errs))
	}
	return b.String(), act, nil
}

func (t *Tools) searchPapers(ctx context.Context, query string) (string, error) {
	cs, err := t.Papers.Search(ctx, query)
	if err != nil {
		return "", err
	}
	if len(cs) == 0 {
		return "No papers found.", nil
	}
	var b strings.Builder
	for _, c := range cs {
		authors := strings.Join(c.Authors, ", ")
		if len(c.Authors) > 4 {
			authors = strings.Join(c.Authors[:3], ", ") + " et al."
		}
		abstract := c.Abstract
		if len(abstract) > 300 {
			abstract = abstract[:300] + "…"
		}
		fmt.Fprintf(&b, "%s — %s (%s, %d). %s\n  %s\n", c.ID, c.Title, authors, c.Year, c.Venue, abstract)
	}
	return b.String(), nil
}

// cite appends the paper to the bibliography the project uses (see refs.BibTarget).
func (t *Tools) cite(ctx context.Context, id string) (string, error) {
	parsed, ok := cite.ParseID(id)
	if !ok {
		return "", fmt.Errorf("%q is not a DOI or an arXiv id", id)
	}
	ref, c, err := t.Papers.Resolve(ctx, parsed)
	if err != nil {
		return "", err
	}
	root, err := os.OpenRoot(t.Dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	got, err := refs.Cite(root, t.MainFile, ref)
	if err != nil {
		return "", err
	}
	if !got.Added {
		return fmt.Sprintf("Already in %s as %s. Cite it with \\cite{%s}.", got.Path, got.Key, got.Key), nil
	}
	msg := fmt.Sprintf("Added %q to %s as %s. Cite it with \\cite{%s}.", c.Title, got.Path, got.Key, got.Key)
	if got.NeedsSetup {
		msg += fmt.Sprintf(" No .tex file loads a bibliography yet: add \\bibliographystyle{plain} and \\bibliography{%s} where the references should appear (or \\addbibresource{%s} with biblatex).", strings.TrimSuffix(got.Load, ".bib"), got.Load)
	}
	return msg, nil
}
