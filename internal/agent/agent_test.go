package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/arxiv"
	"github.com/ponytojas/Refract/internal/cite"
	"github.com/ponytojas/Refract/internal/compile"
	"github.com/ponytojas/Refract/internal/openalex"
	"github.com/ponytojas/Refract/internal/quick"
	"net/http"
	"net/http/httptest"
	"slices"
)

// fakeProvider replays scripted model responses, one per Stream call, and
// records the requests. No real AI calls in tests.
type fakeProvider struct {
	script   [][]ai.Event
	requests []ai.Request
}

func (f *fakeProvider) Status(context.Context) ai.Status { return ai.Status{} }
func (f *fakeProvider) Stream(_ context.Context, req ai.Request) (<-chan ai.Event, error) {
	f.requests = append(f.requests, req)
	if len(f.requests) > len(f.script) {
		return nil, errors.New("unexpected extra model call")
	}
	ch := make(chan ai.Event, 16)
	for _, ev := range f.script[len(f.requests)-1] {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func call(id, name, args string) ai.Event {
	item, _ := json.Marshal(map[string]string{"type": "function_call", "call_id": id, "name": name, "arguments": args})
	return ai.Event{Type: ai.EventItem, Item: item, Call: &ai.FunctionCall{CallID: id, Name: name, Arguments: args}}
}

func say(text string) []ai.Event {
	item, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "content": []map[string]string{{"type": "output_text", "text": text}}})
	return []ai.Event{{Type: ai.EventText, Text: text}, {Type: ai.EventItem, Item: item}, {Type: ai.EventCompleted}}
}

type fakeCompiler struct{ errorsLeft int }

func (c *fakeCompiler) Compile(_ context.Context, dir, main string) (*compile.Result, error) {
	b, _ := os.ReadFile(filepath.Join(dir, main))
	if strings.Contains(string(b), `\oops`) {
		return &compile.Result{Problems: []compile.Problem{{Severity: "error", Kind: "error", File: main, Line: 3, Message: "Undefined control sequence"}}}, nil
	}
	return &compile.Result{OK: true}, nil
}

func setup(t *testing.T) string {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte("\\documentclass{article}\n\\begin{document}\n\\oops Hello\n\\end{document}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644) // like a worktree
	return dir
}

func run(t *testing.T, p *fakeProvider, dir string) (string, []Event, error) {
	t.Helper()
	a := &Agent{Provider: p, Compiler: &fakeCompiler{}, Model: "m", MaxIterations: 5, Timeout: time.Minute}
	var events []Event
	reply, err := a.Run(context.Background(), Input{Dir: dir, MainFile: "main.tex", Message: "fix the compile errors",
		History: []Message{{"user", "earlier question"}, {"assistant", "earlier answer"}}}, func(e Event) { events = append(events, e) })
	return reply, events, err
}

// Scenario 2 of M6: compile, fix, recompile clean.
func TestFixCompileErrorsLoop(t *testing.T) {
	dir := setup(t)
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "compile", `{"include_log":null}`), {Type: ai.EventCompleted}},
		{call("c2", "edit_file", `{"path":"main.tex","old_text":"\\oops Hello","new_text":"Hello"}`), {Type: ai.EventCompleted}},
		{call("c3", "compile", `{"include_log":null}`), {Type: ai.EventCompleted}},
		say("Removed the undefined \\oops command; it compiles now."),
	}}
	reply, events, err := run(t, p, dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "main.tex")); strings.Contains(string(b), `\oops`) {
		t.Fatal("edit not applied")
	}
	var acts []string
	for _, e := range events {
		if e.Type == "activity" {
			acts = append(acts, e.Activity)
		}
	}
	if strings.Join(acts, " | ") != "compiled: 1 error(s) | edited main.tex | compiled: ok" {
		t.Fatalf("activity = %v", acts)
	}
	if !strings.Contains(reply, "compiles now") {
		t.Fatalf("reply = %q", reply)
	}
	// Stateless route: each request replays history, the tool calls and their outputs.
	last := p.requests[3]
	joined := ""
	for _, it := range last.Input {
		joined += string(it) + "\n"
	}
	for _, want := range []string{"earlier answer", `"call_id":"c1"`, "1 error(s)", `"call_id":"c3"`, "Compiled without errors"} {
		if !strings.Contains(joined, want) {
			t.Errorf("last request input lacks %q", want)
		}
	}
	if !strings.Contains(last.Instructions, "main.tex") || strings.Contains(last.Instructions, ".git") {
		t.Errorf("instructions tree wrong:\n%s", last.Instructions)
	}
}

func TestProviderErrorEndsTurn(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{{
		{Type: ai.EventText, Text: "Let me"},
		{Type: ai.EventError, Err: &ai.Error{Kind: ai.ErrUsageLimit, Message: "limit"}},
	}}}
	_, _, err := run(t, p, setup(t))
	var e *ai.Error
	if !errors.As(err, &e) || e.Kind != ai.ErrUsageLimit {
		t.Fatalf("err = %v", err)
	}
}

func TestMaxIterations(t *testing.T) {
	var script [][]ai.Event
	for range 5 {
		script = append(script, []ai.Event{call("c", "list_files", ``), {Type: ai.EventCompleted}})
	}
	if _, _, err := run(t, &fakeProvider{script: script}, setup(t)); !errors.Is(err, ErrTooManySteps) {
		t.Fatalf("err = %v", err)
	}
}

func TestEditFileExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.tex"), []byte("x y x"), 0o644)
	tl := &Tools{Dir: dir}
	cases := []struct{ old, want string }{
		{"x", "matches 2 times"},
		{"z", "not found"},
		{"", "empty"},
	}
	for _, c := range cases {
		out, _ := tl.Call(context.Background(), "edit_file", `{"path":"a.tex","old_text":"`+c.old+`","new_text":"q"}`)
		if !strings.Contains(out, c.want) {
			t.Errorf("old=%q: %q, want %q", c.old, out, c.want)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "a.tex")); string(b) != "x y x" {
		t.Fatal("failed edits changed the file")
	}
	if out, _ := tl.Call(context.Background(), "edit_file", `{"path":"a.tex","old_text":"y","new_text":"Y"}`); !strings.HasPrefix(out, "Edited") {
		t.Fatal(out)
	}
}

func TestToolPathsAreConfined(t *testing.T) {
	dir := t.TempDir()
	tl := &Tools{Dir: dir}
	for _, p := range []string{"../escape.tex", "/etc/passwd", ".git/config", ".refract/x"} {
		out, _ := tl.Call(context.Background(), "write_file", `{"path":"`+p+`","content":"x"}`)
		if !strings.HasPrefix(out, "Error:") {
			t.Errorf("write_file(%q) = %q", p, out)
		}
		out, _ = tl.Call(context.Background(), "read_file", `{"path":"`+p+`","start_line":null,"end_line":null}`)
		if !strings.HasPrefix(out, "Error:") {
			t.Errorf("read_file(%q) = %q", p, out)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.tex")); err == nil {
		t.Fatal("wrote outside the worktree")
	}
}

func TestReadFileRanges(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 500; i++ {
		b.WriteString("line\n")
	}
	os.WriteFile(filepath.Join(dir, "long.tex"), []byte(b.String()), 0o644)
	os.WriteFile(filepath.Join(dir, "img.png"), []byte{0x89, 'P', 'N', 'G', 0}, 0o644)
	tl := &Tools{Dir: dir}
	out, _ := tl.Call(context.Background(), "read_file", `{"path":"long.tex","start_line":null,"end_line":null}`)
	if !strings.HasPrefix(out, "[lines 1-400 of 500]") {
		t.Fatalf("first part header: %q", out[:40])
	}
	out, _ = tl.Call(context.Background(), "read_file", `{"path":"long.tex","start_line":450,"end_line":460}`)
	if !strings.HasPrefix(out, "[lines 450-460 of 500]") || strings.Count(out, "line\n") != 11 {
		t.Fatalf("range: %q", out)
	}
	if out, _ := tl.Call(context.Background(), "read_file", `{"path":"img.png","start_line":null,"end_line":null}`); !strings.Contains(out, "not a text file") {
		t.Fatalf("binary: %q", out)
	}
}

func TestSearch(t *testing.T) {
	if _, err := exec.LookPath("rg"); err != nil {
		t.Skip("ripgrep not installed")
	}
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "sec"), 0o755)
	os.MkdirAll(filepath.Join(dir, ".refract/build"), 0o755)
	os.WriteFile(filepath.Join(dir, "sec/a.tex"), []byte("one\n\\cite{knuth}\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".refract/build/main.log"), []byte("\\cite{knuth}"), 0o644)
	tl := &Tools{Dir: dir}
	out, _ := tl.Call(context.Background(), "search", `{"query":"\\cite{knuth}","glob":null}`)
	if out != `sec/a.tex:2:\cite{knuth}` {
		t.Fatalf("search = %q", out)
	}
	if out, _ := tl.Call(context.Background(), "search", `{"query":"absent","glob":"*.tex"}`); out != "No matches." {
		t.Fatalf("no match = %q", out)
	}
}

func TestEffortIsSent(t *testing.T) {
	for _, effort := range []string{"", "high"} {
		p := &fakeProvider{script: [][]ai.Event{say("ok")}}
		a := &Agent{Provider: p, Compiler: &fakeCompiler{}, Model: "m", Effort: effort, MaxIterations: 1, Timeout: time.Minute}
		if _, err := a.Run(context.Background(), Input{Dir: setup(t), Message: "hi"}, func(Event) {}); err != nil {
			t.Fatal(err)
		}
		got := p.requests[0].Reasoning
		if (effort == "") != (got == nil) || (got != nil && got.Effort != effort) {
			t.Errorf("effort %q: request reasoning = %+v", effort, got)
		}
	}
}

func TestCitePaper(t *testing.T) {
	body, err := os.ReadFile("../arxiv/testdata/search.xml") // a real arXiv answer; Get uses its first paper
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(body) }))
	defer srv.Close()
	dir := setup(t)
	os.WriteFile(filepath.Join(dir, "main.tex"), []byte("\\bibliography{refs}\n"), 0o644)
	papers := &cite.Service{Arxiv: &arxiv.Client{HTTP: srv.Client(), BaseURL: srv.URL}, OpenAlex: &openalex.Client{HTTP: srv.Client(), BaseURL: srv.URL}}
	tl := &Tools{Dir: dir, MainFile: "main.tex", Papers: papers}

	if !slices.ContainsFunc(tl.Definitions(), func(d ai.Tool) bool { return d.Name == "cite" }) {
		t.Fatal("cite not offered")
	}
	out, _ := tl.Call(context.Background(), "cite", `{"id":"https://arxiv.org/abs/2604.21816v1"}`)
	if !strings.Contains(out, `\cite{sadani2026tool}`) || !strings.Contains(out, "refs.bib") {
		t.Fatalf("cite = %q", out)
	}
	bib, _ := os.ReadFile(filepath.Join(dir, "refs.bib"))
	if !strings.Contains(string(bib), "@misc{sadani2026tool,") || !strings.Contains(string(bib), "eprint = {2604.21816}") {
		t.Errorf("refs.bib =\n%s", bib)
	}
	if out, _ := tl.Call(context.Background(), "cite", `{"id":"2604.21816"}`); !strings.HasPrefix(out, "Already in refs.bib") {
		t.Errorf("second cite = %q", out)
	}
	if out, _ := tl.Call(context.Background(), "cite", `{"id":"../../etc"}`); !strings.HasPrefix(out, "Error:") {
		t.Errorf("bad id = %q", out)
	}
	if slices.ContainsFunc((&Tools{Dir: dir}).Definitions(), func(d ai.Tool) bool { return d.Name == "search_papers" }) {
		t.Error("paper tools offered without a service")
	}
}

func lastInstructions(t *testing.T, dir string) string {
	t.Helper()
	p := &fakeProvider{script: [][]ai.Event{{{Type: ai.EventText, Text: "done"}}}}
	if _, _, err := run(t, p, dir); err != nil {
		t.Fatal(err)
	}
	return p.requests[len(p.requests)-1].Instructions
}

func TestProjectInstructionsInPrompt(t *testing.T) {
	dir := setup(t)
	if got := lastInstructions(t, dir); strings.Contains(got, "Project instructions from the author") {
		t.Errorf("heading without REFRACT.md:\n%s", got)
	}
	os.WriteFile(filepath.Join(dir, "REFRACT.md"), []byte("<!-- template -->\n"), 0o644)
	if got := lastInstructions(t, dir); strings.Contains(got, "Project instructions from the author") {
		t.Errorf("heading for a comment-only REFRACT.md:\n%s", got)
	}
	os.WriteFile(filepath.Join(dir, "REFRACT.md"), []byte("Use British spelling.\n"), 0o644)
	if got := lastInstructions(t, dir); !strings.HasSuffix(got, quick.InstructionsHeading+"Use British spelling.") {
		t.Errorf("REFRACT.md missing:\n%s", got)
	}
	os.WriteFile(filepath.Join(dir, "REFRACT.md"), []byte(strings.Repeat("ü", 6000)), 0o644) // 12 KB
	got := lastInstructions(t, dir)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "[REFRACT.md is longer than 8 KB; only its beginning is included.]") {
		t.Errorf("not cut at 8 KB: …%q", got[len(got)-100:])
	}
}

func TestProjectInstructionsStayInTheWorktree(t *testing.T) {
	dir := setup(t)
	outside := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(outside, []byte("server secret"), 0o644)
	if err := os.Symlink(outside, filepath.Join(dir, "REFRACT.md")); err != nil {
		t.Skip(err)
	}
	if got := lastInstructions(t, dir); strings.Contains(got, "server secret") {
		t.Error("REFRACT.md followed a symlink out of the worktree")
	}
}
