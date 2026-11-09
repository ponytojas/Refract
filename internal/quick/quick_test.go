package quick

import (
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/ponytojas/Refract/internal/ai"
)

func TestValidate(t *testing.T) {
	ok := Request{Action: "improve", Selection: "Some text."}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	for name, r := range map[string]Request{
		"unknown action":    {Action: "delete", Selection: "x"},
		"empty selection":   {Action: "improve", Selection: " \n"},
		"long selection":    {Action: "improve", Selection: strings.Repeat("a", MaxSelection+1)},
		"long context":      {Action: "improve", Selection: "x", Before: strings.Repeat("a", MaxContext+1)},
		"custom, no prompt": {Action: "custom", Selection: "x"},
	} {
		if r.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPromptMarksTheSelectionInPlace(t *testing.T) {
	instr, input := Prompt(Request{Action: "custom", Instruction: "Use the passive voice", Selection: "We ran it.", Before: "Intro. ", After: " Then.", File: "main.tex"})
	if !strings.HasSuffix(instr, "Task: Use the passive voice") {
		t.Errorf("instructions = %q", instr)
	}
	if !strings.Contains(input, "Intro. <selection>We ran it.</selection> Then.") || !strings.Contains(input, "File: main.tex") {
		t.Errorf("input = %q", input)
	}
	instr, _ = Prompt(Request{Action: "explain", Selection: "x"})
	if strings.Contains(instr, "Task:") {
		t.Errorf("explain uses the rewrite rules: %q", instr)
	}
}

func TestClean(t *testing.T) {
	for _, c := range []struct{ sel, reply, want string }{
		{"Hello world.", "Hello, world.", "Hello, world."},
		// Surrounding whitespace of the selection survives: no glued words or lines.
		{" two words\n", "Two words.", " Two words.\n"},
		{"\n\nPara one.\n\nPara two.\n", "  Para one.\n\nPara two.  ", "\n\nPara one.\n\nPara two.\n"},
		// Fences and echoed tags the model was told not to send.
		{"a+b", "```latex\na + b\n```", "a + b"},
		{"x", "<selection>y</selection>", "y"},
		// Inside the reply, line structure is kept.
		{"line 1\nline 2", "Line 1\nLine 2", "Line 1\nLine 2"},
	} {
		if got := Clean(c.sel, c.reply); got != c.want {
			t.Errorf("Clean(%q, %q) = %q, want %q", c.sel, c.reply, got, c.want)
		}
	}
}

type fake struct {
	events []ai.Event
	req    ai.Request
}

func (f *fake) Status(context.Context) ai.Status { return ai.Status{} }
func (f *fake) Stream(_ context.Context, req ai.Request) (<-chan ai.Event, error) {
	f.req = req
	ch := make(chan ai.Event, len(f.events))
	for _, e := range f.events {
		ch <- e
	}
	close(ch)
	return ch, nil
}

func TestRun(t *testing.T) {
	p := &fake{events: []ai.Event{{Type: ai.EventText, Text: "Better "}, {Type: ai.EventText, Text: "text."}, {Type: ai.EventCompleted}}}
	got, err := Run(context.Background(), p, "m", "low", Request{Action: "improve", Selection: " Bad text. "})
	if err != nil || got != " Better text. " {
		t.Fatalf("Run = %q, %v", got, err)
	}
	if p.req.Model != "m" || p.req.Reasoning == nil || p.req.Reasoning.Effort != "low" {
		t.Errorf("request = %+v", p.req)
	}

	p = &fake{events: []ai.Event{{Type: ai.EventText, Text: "It means x.\n"}, {Type: ai.EventCompleted}}}
	if got, _ := Run(context.Background(), p, "m", "", Request{Action: "explain", Selection: " x "}); got != "It means x." {
		t.Errorf("explain = %q", got)
	}

	p = &fake{events: []ai.Event{{Type: ai.EventError, Err: &ai.Error{Kind: ai.ErrUsageLimit, Message: "limit"}}}}
	var e *ai.Error
	if _, err := Run(context.Background(), p, "m", "", Request{Action: "improve", Selection: "x"}); !errors.As(err, &e) || e.Kind != ai.ErrUsageLimit {
		t.Errorf("err = %v", err)
	}

	p = &fake{events: []ai.Event{{Type: ai.EventText, Text: "half"}}} // stream cut before completed
	if _, err := Run(context.Background(), p, "m", "", Request{Action: "improve", Selection: "x"}); err == nil {
		t.Error("incomplete reply accepted")
	}
}

func TestProjectInstructions(t *testing.T) {
	if got := ProjectInstructions("<!-- Write here -->\n\n## Style\n<!-- e.g. British -->\n", 100); got != "## Style" {
		t.Errorf("comments not dropped: %q", got)
	}
	if got := ProjectInstructions("  <!-- only a comment -->  ", 100); got != "" {
		t.Errorf("comment-only file = %q, want empty", got)
	}
	if got := ProjectInstructions("keep <!-- unterminated", 100); got != "keep" {
		t.Errorf("unterminated comment = %q", got)
	}
	long := strings.Repeat("é", 2000) // 4000 bytes
	got := ProjectInstructions(long, 2<<10)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "[REFRACT.md is longer than 2 KB; only its beginning is included.]") {
		t.Errorf("cut = …%q", got[len(got)-80:])
	}
	if body, _, _ := strings.Cut(got, "\n["); len(body) > 2<<10 {
		t.Errorf("cut keeps %d bytes", len(body))
	}
}

func TestPromptIncludesProjectInstructions(t *testing.T) {
	for _, action := range []string{"improve", "explain"} {
		instr, input := Prompt(Request{Action: action, Selection: "x", ProjectInstructions: "Use British spelling."})
		if !strings.HasSuffix(instr, InstructionsHeading+"Use British spelling.") || strings.Contains(input, "British") {
			t.Errorf("%s: instructions = %q", action, instr)
		}
	}
	instr, _ := Prompt(Request{Action: "improve", Selection: "x", ProjectInstructions: strings.Repeat("a", 5000)})
	if !strings.Contains(instr, "longer than 2 KB") {
		t.Error("quick actions don't cap the instructions at 2 KB")
	}
	instr, _ = Prompt(Request{Action: "improve", Selection: "x", ProjectInstructions: "<!-- template -->"})
	if strings.Contains(instr, "Project instructions") {
		t.Error("an empty REFRACT.md adds a heading")
	}
	if err := (Request{Action: "improve", Selection: "x", ProjectInstructions: strings.Repeat("a", MaxProjectInstructions+1)}).Validate(); err == nil {
		t.Error("oversized REFRACT.md accepted")
	}
}
