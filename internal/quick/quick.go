// Package quick runs one-shot edits on an editor selection: a single model
// call, no agent loop, no worktree. The editor shows the result as an inline
// diff and applies it like a normal edit.
package quick

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/ponytojas/Refract/internal/ai"
)

// Request is one quick action on a selection.
type Request struct {
	Action      string `json:"action"`      // see tasks; "custom" uses Instruction
	Instruction string `json:"instruction"` // custom only
	Selection   string `json:"selection"`
	Before      string `json:"before"` // text just before the selection, for context
	After       string `json:"after"`  // text just after it
	File        string `json:"file"`
	// ProjectInstructions is the project's REFRACT.md as the editor has it.
	// The client sends it because quick actions aren't tied to a project.
	ProjectInstructions string `json:"projectInstructions"`
}

const (
	MaxSelection   = 20000
	MaxContext     = 4000 // per side
	MaxInstruction = 2000
	// MaxProjectInstructions bounds what the client may send; only the first
	// QuickInstructions bytes reach the model.
	MaxProjectInstructions = 16 << 10
	QuickInstructions      = 2 << 10
)

// InstructionsFile is the file at the project root whose text the assistant
// and quick actions always follow.
const InstructionsFile = "REFRACT.md"

var htmlComment = regexp.MustCompile(`(?s)<!--.*?(-->|$)`)

// ProjectInstructions prepares the text of REFRACT.md for a prompt: it drops
// HTML comments (the template is only comments, so it counts as empty), trims
// it, and cuts it to max bytes on a character boundary with a note saying so.
// It returns "" when nothing is left.
func ProjectInstructions(text string, max int) string {
	text = strings.TrimSpace(htmlComment.ReplaceAllString(text, ""))
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return strings.TrimSpace(text[:cut]) + fmt.Sprintf("\n[%s is longer than %d KB; only its beginning is included.]", InstructionsFile, max>>10)
}

// InstructionsHeading introduces the author's instructions after the rules,
// which they never override.
const InstructionsHeading = "\n\nProject instructions from the author (" + InstructionsFile + "). Follow them unless they conflict with the rules above:\n"

var tasks = map[string]string{
	"improve": "Improve the wording: clearer, more fluent and more precise. Keep the meaning, the tone and roughly the length.",
	"shorten": "Make it shorter and more concise without losing information.",
	"grammar": "Fix spelling, grammar and punctuation only. Change nothing else.",
	"table": "Convert it into a LaTeX table: a table environment with a tabular, a \\caption and a \\label. " +
		"Use \\toprule, \\midrule and \\bottomrule only if the surrounding text already uses booktabs; otherwise use \\hline. " +
		"Lay it out over several lines, one row per line, indented like the surrounding code.",
	"explain": "",
	"custom":  "",
}

// Explain reports whether the action answers in prose instead of replacing the selection.
func (r Request) Explain() bool { return r.Action == "explain" }

// Validate checks the request at the API boundary.
func (r Request) Validate() error {
	if _, ok := tasks[r.Action]; !ok {
		return fmt.Errorf("unknown action %q", r.Action)
	}
	if strings.TrimSpace(r.Selection) == "" {
		return errors.New("select some text first")
	}
	if len(r.Selection) > MaxSelection {
		return fmt.Errorf("selection too long (max %d characters)", MaxSelection)
	}
	if len(r.Before) > MaxContext || len(r.After) > MaxContext {
		return fmt.Errorf("context too long (max %d characters per side)", MaxContext)
	}
	if r.Action == "custom" && strings.TrimSpace(r.Instruction) == "" {
		return errors.New("write an instruction")
	}
	if len(r.Instruction) > MaxInstruction {
		return fmt.Errorf("instruction too long (max %d characters)", MaxInstruction)
	}
	if len(r.ProjectInstructions) > MaxProjectInstructions {
		return fmt.Errorf("%s too long (max %d KB)", InstructionsFile, MaxProjectInstructions>>10)
	}
	return nil
}

const rewriteRules = `You edit one fragment of a LaTeX document. The fragment is between <selection> tags; the text around it is context only.
Reply with ONLY the text that replaces the fragment: no explanations, no quotes, no code fences, no tags.
Rules:
- The reply must fit exactly where the fragment was and keep the document valid LaTeX.
- Keep every command, environment, label, citation, reference and math expression the fragment needs, unless the task is about them.
- If the fragment is inside math, reply with math content only: do not add $, \( \) or math environments.
- Keep the fragment's language. Keep its line structure (a one-line fragment stays one line) unless the task asks for a new structure.
- Never repeat or change the context outside the fragment.

Task: `

const explainRules = `You help the author of a LaTeX document understand one fragment of it. The fragment is between <selection> tags; the text around it is context.
Explain what the fragment says or does (its LaTeX commands, its math, its argument), briefly: a few sentences or a short list in Markdown.
Answer in the language the fragment is written in.`

// Prompt builds the instructions and the user input for a request.
func Prompt(r Request) (instructions, input string) {
	if r.Explain() {
		instructions = explainRules
	} else {
		task := tasks[r.Action]
		if r.Action == "custom" {
			task = strings.TrimSpace(r.Instruction)
		}
		instructions = rewriteRules + task
	}
	if pi := ProjectInstructions(r.ProjectInstructions, QuickInstructions); pi != "" {
		instructions += InstructionsHeading + pi
	}
	var b strings.Builder
	if r.File != "" {
		fmt.Fprintf(&b, "File: %s\n\n", r.File)
	}
	fmt.Fprintf(&b, "Context before the fragment:\n%s<selection>%s</selection>%s\n(context after the fragment ends here)", r.Before, r.Selection, r.After)
	return instructions, b.String()
}

var fence = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n(.*?)\\n?```$")

// Clean turns the model's reply into the replacement text: it drops code
// fences and echoed tags the model was told not to send, and keeps the
// selection's leading and trailing whitespace so the edit never glues words
// or lines together.
func Clean(selection, reply string) string {
	out := strings.TrimSpace(reply)
	if m := fence.FindStringSubmatch(out); m != nil {
		out = strings.TrimSpace(m[1])
	}
	out = strings.TrimPrefix(out, "<selection>")
	out = strings.TrimSuffix(out, "</selection>")
	out = strings.TrimSpace(out)
	lead := selection[:len(selection)-len(strings.TrimLeft(selection, " \t\r\n"))]
	trail := selection[len(strings.TrimRight(selection, " \t\r\n")):]
	return lead + out + trail
}

// Run makes the model call and returns the replacement (or the explanation).
func Run(ctx context.Context, p ai.Provider, model, effort string, r Request) (string, error) {
	instructions, input := Prompt(r)
	req := ai.Request{Model: model, Instructions: instructions, Input: []json.RawMessage{ai.UserMessage(input)}}
	if effort != "" {
		req.Reasoning = &ai.Reasoning{Effort: effort}
	}
	text, err := ai.Complete(ctx, p, req)
	if err != nil {
		return "", err
	}
	if r.Explain() {
		return strings.TrimSpace(text), nil
	}
	return Clean(r.Selection, text), nil
}
