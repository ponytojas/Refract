package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/cite"
	"github.com/ponytojas/Refract/internal/compile"
	"github.com/ponytojas/Refract/internal/quick"
)

// Agent runs one assistant turn.
type Agent struct {
	Provider      ai.Provider
	Compiler      compile.Compiler
	Model         string
	Effort        string        // reasoning effort; empty: the model's default
	MaxIterations int           // model calls per turn (default 25)
	Timeout       time.Duration // whole turn
	Papers        *cite.Service // enables search_papers and cite; nil to leave them out
}

// Message is one earlier exchange replayed as context.
type Message struct {
	Role string `json:"role"` // user | assistant
	Text string `json:"content"`
}

// Input is everything a turn needs.
type Input struct {
	Dir      string // the turn's worktree
	MainFile string
	History  []Message
	Message  string // the user's request, with the open file and selection attached
}

// Event is streamed to the chat panel.
type Event struct {
	Type     string    `json:"type"` // text | reasoning | activity
	Text     string    `json:"text,omitempty"`
	Activity string    `json:"activity,omitempty"`
	Error    *ai.Error `json:"error,omitempty"`
}

// ErrTooManySteps ends a turn that hit MaxIterations.
var ErrTooManySteps = errors.New("the assistant used too many steps and was stopped")

const instructions = `You are a LaTeX research-writing assistant working inside the user's project.

Rules:
- Make minimal, targeted edits. Keep the author's voice, structure and macros.
- Never invent citations, bibliography entries, references, data or results. If something needs a source you don't have, say so.
- To cite a paper, find it with search_papers and add it with cite, which writes the BibTeX from the publisher's or arXiv's metadata; then use the key it returns. Never write a bibliography entry yourself, and only cite papers whose abstract supports the claim.
- Read files before editing them. Use edit_file for changes to existing files; old_text must match exactly once.
- After non-trivial edits, compile and fix any errors you introduced. Don't try to fix pre-existing problems unless asked.
- Your changes are proposed to the user as a diff; they decide whether to apply them.
- When the user asks you to remember something for this project, propose an edit to REFRACT.md at the project root (create it with write_file if it doesn't exist). Never store it anywhere else.
- Finish with a short summary of what you changed and why. Reply in the language the user writes in.

The project's main file is %s.
Project files:
%s`

// Run executes the turn: model calls and tool calls until the model answers
// without calling tools. It returns everything the model said.
func (a *Agent) Run(ctx context.Context, in Input, emit func(Event)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, a.Timeout)
	defer cancel()
	tools := &Tools{Dir: in.Dir, MainFile: in.MainFile, Compiler: a.Compiler, Papers: a.Papers}
	tree, _ := tools.listFiles()

	req := ai.Request{
		Model:        a.Model,
		Instructions: fmt.Sprintf(instructions, cmpOr(in.MainFile, "(not set)"), tree),
		Tools:        tools.Definitions(),
	}
	if pi := projectInstructions(in.Dir); pi != "" {
		req.Instructions += quick.InstructionsHeading + pi
	}
	if a.Effort != "" {
		req.Reasoning = &ai.Reasoning{Effort: a.Effort, Summary: "auto"}
	}
	for _, m := range in.History {
		b, _ := json.Marshal(m)
		req.Input = append(req.Input, b)
	}
	req.Input = append(req.Input, ai.UserMessage(in.Message))

	var reply strings.Builder
	for range a.MaxIterations {
		ch, err := a.Provider.Stream(ctx, req)
		if err != nil {
			return reply.String(), err
		}
		var calls []*ai.FunctionCall
		for ev := range ch {
			switch ev.Type {
			case ai.EventText:
				reply.WriteString(ev.Text)
				emit(Event{Type: "text", Text: ev.Text})
			case ai.EventReasoning:
				emit(Event{Type: "reasoning", Text: ev.Text})
			case ai.EventItem:
				if len(ev.Item) > 0 {
					req.Input = append(req.Input, ev.Item) // replayed: the route is stateless
				}
				if ev.Call != nil {
					calls = append(calls, ev.Call)
				}
			case ai.EventError:
				return reply.String(), ev.Err
			}
		}
		if ctx.Err() != nil {
			return reply.String(), ctx.Err()
		}
		if len(calls) == 0 {
			return reply.String(), nil
		}
		if reply.Len() > 0 && !strings.HasSuffix(reply.String(), "\n") {
			reply.WriteString("\n")
			emit(Event{Type: "text", Text: "\n"})
		}
		for _, c := range calls {
			out, act := tools.Call(ctx, c.Name, c.Arguments)
			emit(Event{Type: "activity", Activity: act})
			req.Input = append(req.Input, ai.FunctionOutput(c.CallID, out))
		}
	}
	return reply.String(), ErrTooManySteps
}

// MaxInstructions caps how much of REFRACT.md goes into a turn's prompt.
const MaxInstructions = 8 << 10

// projectInstructions reads REFRACT.md from the worktree, confined to it so a
// symlink can't pull in files from elsewhere. Missing or unreadable means none.
func projectInstructions(dir string) string {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return ""
	}
	defer root.Close()
	f, err := root.Open(quick.InstructionsFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return ""
	}
	return quick.ProjectInstructions(string(b), MaxInstructions)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
