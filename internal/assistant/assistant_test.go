package assistant

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/agent"
	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/auth"
	"github.com/ponytojas/Refract/internal/compile"
	"github.com/ponytojas/Refract/internal/projects"
	"github.com/ponytojas/Refract/internal/store"
)

// fakeProvider replays one scripted response per model call. before, if set,
// runs at the start of call n (to simulate the user editing during a turn).
type fakeProvider struct {
	mu      sync.Mutex
	script  [][]ai.Event
	calls   int
	before  map[int]func()
	block   chan struct{} // if set, Stream waits on it (to test Stop)
	entered chan struct{} // closed when Stream is first called
}

func (f *fakeProvider) Status(context.Context) ai.Status { return ai.Status{} }
func (f *fakeProvider) Stream(ctx context.Context, _ ai.Request) (<-chan ai.Event, error) {
	f.mu.Lock()
	n := f.calls
	f.calls++
	f.mu.Unlock()
	if fn := f.before[n]; fn != nil {
		fn()
	}
	if f.entered != nil && n == 0 {
		close(f.entered)
	}
	if f.block != nil {
		select {
		case <-f.block:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ch := make(chan ai.Event, 16)
	for _, ev := range f.script[n] {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func call(id, name, args string) ai.Event {
	item, _ := json.Marshal(map[string]string{"type": "function_call", "call_id": id, "name": name, "arguments": args})
	return ai.Event{Type: ai.EventItem, Item: item, Call: &ai.FunctionCall{CallID: id, Name: name, Arguments: args}}
}

func done(text string) []ai.Event {
	return []ai.Event{{Type: ai.EventText, Text: text}, {Type: ai.EventCompleted}}
}

type okCompiler struct{}

func (okCompiler) Compile(context.Context, string, string) (*compile.Result, error) {
	return &compile.Result{OK: true}, nil
}

const mainTex = "\\documentclass{article}\n\\begin{document}\n\\section{Introduction}\nIntro text.\n\n\\section{Method}\nMethod text.\n\\end{document}\n"

// ownerCtx is the signed-in user the tests act as. Who may reach a chat or
// turn is checked by the route table (cmd/refract/routes_test.go).
var ownerCtx = auth.WithUser(context.Background(), auth.User{ID: "owner"})

func setup(t *testing.T, p *fakeProvider) (*Service, *projects.Service, string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	ps := &projects.Service{DB: db, Dir: filepath.Join(dir, "projects"), Limits: projects.DefaultLimits}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("main.tex")
	w.Write([]byte(mainTex))
	zw.Close()
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	res, err := ps.Import(ownerCtx, "paper", projects.ZipSource(zr))
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{
		DB: db, Projects: ps, Access: &access.Checker{DB: db},
		Agent:    agent.Agent{Provider: p, Compiler: okCompiler{}, MaxIterations: 10, Timeout: time.Minute},
		ModelFor: func(_ context.Context, c Choice) (Choice, error) { return Choice{Model: cmp.Or(c.Model, "m")}, nil },
	}
	c, err := s.CreateChat(ownerCtx, res.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, ps, res.Project.ID, c.ID
}

func commits(t *testing.T, ps *projects.Service, id string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", ps.Path(id), "log", "--format=%s").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

func file(t *testing.T, ps *projects.Service, id, rel string) string {
	b, _ := os.ReadFile(filepath.Join(ps.Path(id), rel))
	return string(b)
}

var addRelatedWork = `{"path":"main.tex","old_text":"Intro text.\n","new_text":"Intro text.\n\n\\subsection{Related work}\nPrior work.\n"}`

// Scenario 1: a request becomes a reviewable diff; accept creates one commit.
func TestScenarioAcceptCreatesOneCommit(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "read_file", `{"path":"main.tex","start_line":null,"end_line":null}`), {Type: ai.EventCompleted}},
		{call("c2", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
		{call("c3", "compile", `{"include_log":null}`), {Type: ai.EventCompleted}},
		done("Added a Related work subsection after the introduction."),
	}}
	s, ps, id, chat := setup(t, p)
	ctx := ownerCtx
	var activity []string
	turn, err := s.Ask(ctx, chat, "Add a related-work subsection after the introduction", Context{OpenFile: "main.tex"}, Choice{}, func(e agent.Event) {
		if e.Type == "activity" {
			activity = append(activity, e.Activity)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != "review" || len(turn.Files) != 1 || turn.Files[0].Path != "main.tex" {
		t.Fatalf("turn = %+v", turn)
	}
	if strings.Join(activity, ",") != "read main.tex,edited main.tex,compiled: ok" {
		t.Fatalf("activity = %v", activity)
	}
	if file(t, ps, id, "main.tex") != mainTex {
		t.Fatal("project changed before accept")
	}
	diff, err := s.Diff(ctx, turn.ID)
	if err != nil || len(diff) != 1 || diff[0].Before != mainTex || !strings.Contains(diff[0].After, "Related work") {
		t.Fatalf("diff = %+v %v", diff, err)
	}

	before := len(commits(t, ps, id))
	turn, conflicts, err := s.Accept(ctx, turn.ID, nil)
	if err != nil || conflicts != nil || turn.Status != "accepted" || len(turn.Commits) != 1 {
		t.Fatalf("accept: %+v %v %v", turn, conflicts, err)
	}
	log := commits(t, ps, id)
	if len(log) != before+1 || !strings.HasPrefix(log[0], "ai: Add a related-work") {
		t.Fatalf("log = %v", log)
	}
	if !strings.Contains(file(t, ps, id, "main.tex"), "\\subsection{Related work}") {
		t.Fatal("change not applied")
	}
	// Persisted: survives a restart (a new Service on the same DB).
	turns, _ := (&Service{DB: s.DB, Projects: ps}).Turns(ctx, chat)
	if len(turns) != 1 || turns[0].Status != "accepted" || !strings.Contains(turns[0].Reply, "Related work") {
		t.Fatalf("persisted turns = %+v", turns)
	}
}

// Scenario 3: reject leaves the project and its history untouched.
func TestScenarioRejectLeavesNoTrace(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
		done("private answer after revocation"),
	}}
	s, ps, id, chat := setup(t, p)
	ctx := ownerCtx
	turn, err := s.Ask(ctx, chat, "add related work", Context{}, Choice{}, func(agent.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	logBefore := commits(t, ps, id)
	if turn, err = s.Reject(ctx, turn.ID); err != nil || turn.Status != "rejected" {
		t.Fatal(turn, err)
	}
	if got := commits(t, ps, id); strings.Join(got, "|") != strings.Join(logBefore, "|") {
		t.Fatalf("history changed: %v", got)
	}
	if file(t, ps, id, "main.tex") != mainTex {
		t.Fatal("project changed")
	}
	if ents, _ := os.ReadDir(filepath.Join(ps.Path(id), ".refract", "worktrees")); len(ents) != 0 {
		t.Fatalf("worktree left: %v", ents)
	}
	if out, _ := exec.Command("git", "-C", ps.Path(id), "branch", "--list", "ai/*").Output(); len(out) != 0 {
		t.Fatalf("branch left: %s", out)
	}
	if _, _, err := s.Accept(ctx, turn.ID, nil); !errors.Is(err, ErrState) {
		t.Fatalf("accept after reject: %v", err)
	}
}

// Scenario 4: the user edits during the turn; accept merges cleanly or reports a conflict.
func TestScenarioUserEditsDuringTurn(t *testing.T) {
	ctx := ownerCtx
	t.Run("different lines merge", func(t *testing.T) {
		p := &fakeProvider{script: [][]ai.Event{{call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}}, done("ok")}}
		s, ps, id, chat := setup(t, p)
		p.before = map[int]func(){1: func() {
			ps.WriteFile(ctx, id, "main.tex", strings.NewReader(strings.Replace(mainTex, "Method text.", "Method text, edited by hand.", 1)))
		}}
		turn, err := s.Ask(ctx, chat, "add related work", Context{}, Choice{}, func(agent.Event) {})
		if err != nil {
			t.Fatal(err)
		}
		if _, conflicts, err := s.Accept(ctx, turn.ID, nil); err != nil || conflicts != nil {
			t.Fatal(conflicts, err)
		}
		got := file(t, ps, id, "main.tex")
		if !strings.Contains(got, "Related work") || !strings.Contains(got, "edited by hand") {
			t.Fatalf("merge lost a side:\n%s", got)
		}
	})
	t.Run("same line conflicts", func(t *testing.T) {
		aiRewrite := `{"path":"main.tex","old_text":"Intro text.","new_text":"Intro text, improved by AI."}`
		p := &fakeProvider{script: [][]ai.Event{{call("c1", "edit_file", aiRewrite), {Type: ai.EventCompleted}}, done("ok")}}
		s, ps, id, chat := setup(t, p)
		mine := strings.Replace(mainTex, "Intro text.", "Intro text, rewritten by me.", 1)
		p.before = map[int]func(){1: func() { ps.WriteFile(ctx, id, "main.tex", strings.NewReader(mine)) }}
		turn, err := s.Ask(ctx, chat, "add related work", Context{}, Choice{}, func(agent.Event) {})
		if err != nil {
			t.Fatal(err)
		}
		turn, conflicts, err := s.Accept(ctx, turn.ID, nil)
		if err != nil || len(conflicts) != 1 || conflicts[0] != "main.tex" {
			t.Fatalf("want a conflict on main.tex: %v %v", conflicts, err)
		}
		if file(t, ps, id, "main.tex") != mine {
			t.Fatal("conflict touched the user's file")
		}
		if turn.Status != "review" {
			t.Fatalf("turn should stay reviewable, got %s", turn.Status)
		}
	})
}

// Scenario 5: a usage-limit error is reported clearly and nothing is applied.
func TestScenarioUsageLimit(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
		{{Type: ai.EventError, Err: &ai.Error{Kind: ai.ErrUsageLimit, Code: "subscription_sharing_usage_limit_exceeded", Message: "limit"}}},
	}}
	s, ps, id, chat := setup(t, p)
	logBefore := commits(t, ps, id)
	turn, err := s.Ask(ownerCtx, chat, "add related work", Context{}, Choice{}, func(agent.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != "failed" || turn.Error == nil || turn.Error.Kind != ai.ErrUsageLimit || len(turn.Files) != 0 {
		t.Fatalf("turn = %+v", turn)
	}
	if file(t, ps, id, "main.tex") != mainTex || len(commits(t, ps, id)) != len(logBefore) {
		t.Fatal("partial changes applied")
	}
	if ents, _ := os.ReadDir(filepath.Join(ps.Path(id), ".refract", "worktrees")); len(ents) != 0 {
		t.Fatalf("worktree left: %v", ents)
	}
}

func TestStopAndBusy(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{done("never")}, block: make(chan struct{}), entered: make(chan struct{})}
	s, ps, id, chat := setup(t, p)
	ctx, cancel := context.WithCancel(ownerCtx)
	result := make(chan *Turn)
	go func() {
		turn, _ := s.Ask(ctx, chat, "slow", Context{}, Choice{}, func(agent.Event) {})
		result <- turn
	}()
	<-p.entered // the model is "thinking"
	if _, err := s.Ask(ownerCtx, chat, "second", Context{}, Choice{}, func(agent.Event) {}); !errors.Is(err, ErrBusy) {
		t.Fatalf("concurrent turn: %v", err)
	}
	cancel() // Stop
	turn := <-result
	if turn.Status != "stopped" || file(t, ps, id, "main.tex") != mainTex {
		t.Fatalf("turn = %+v", turn)
	}
}

func TestRecoverMarksRunningTurnsStopped(t *testing.T) {
	s, _, _, chat := setup(t, &fakeProvider{})
	ctx := ownerCtx
	s.save(ctx, &Turn{ID: "t1", ChatID: chat, Message: "x", Status: "running", CreatedAt: time.Now()})
	if err := s.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	turns, _ := s.Turns(ctx, chat)
	if len(turns) != 1 || turns[0].Status != "stopped" {
		t.Fatalf("turns = %+v", turns)
	}
}

func TestContextAndHistory(t *testing.T) {
	msg := withContext("Shorten this", Context{OpenFile: "intro.tex", Selection: "Long sentence.", FromLine: 3, ToLine: 4})
	if !strings.Contains(msg, "[Open file: intro.tex]") || !strings.Contains(msg, "lines 3-4 of intro.tex") || !strings.Contains(msg, "Long sentence.") {
		t.Fatalf("message = %q", msg)
	}
	if got := title("A very long first line that goes on and on and on and on and on and on\nsecond"); len([]rune(got)) != 61 {
		t.Fatalf("title = %q", got)
	}
}

// Chats and turns follow their project's owner: another user can't list,
// read, continue, review or accept them.

// A chat remembers the model it used, and a turn keeps the model's reasoning.
func TestChatModelAndReasoning(t *testing.T) {
	s, _, projectID, chatID := setup(t, &fakeProvider{script: [][]ai.Event{{
		{Type: ai.EventReasoning, Text: "Reading the "},
		{Type: ai.EventReasoning, Text: "introduction."},
		{Type: ai.EventText, Text: "Looks fine."},
		{Type: ai.EventCompleted},
	}}})
	ctx := ownerCtx
	var streamed string
	turn, err := s.Ask(ctx, chatID, "Check the intro", Context{}, Choice{Model: "anthropic:claude-x", Effort: "high"}, func(e agent.Event) {
		if e.Type == "reasoning" {
			streamed += e.Text
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if streamed != "Reading the introduction." || turn.Reasoning != streamed {
		t.Fatalf("streamed %q, turn %q", streamed, turn.Reasoning)
	}
	turns, _ := s.Turns(ctx, chatID)
	if turns[0].Reasoning != streamed {
		t.Fatalf("stored reasoning %q", turns[0].Reasoning)
	}
	chats, _ := s.Chats(ctx, projectID)
	if chats[0].Model != "anthropic:claude-x" {
		t.Fatalf("chat %+v", chats[0])
	}
}

// onStream runs before each model call, to change things while a turn runs.
type onStream struct {
	ai.Provider
	before func()
}

func (o onStream) Stream(ctx context.Context, r ai.Request) (<-chan ai.Event, error) {
	o.before()
	return o.Provider.Stream(ctx, r)
}

// A turn runs for minutes; if the project stops being the user's meanwhile,
// nothing it did is kept.
func TestTurnRechecksAccessBeforeWriting(t *testing.T) {
	p := &fakeProvider{script: [][]ai.Event{
		{call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
		done("private answer after revocation"),
	}}
	s, ps, id, chat := setup(t, p)
	before := len(commits(t, ps, id))
	const chatUpdated = 1
	s.Agent.Provider = onStream{p, func() {
		ps.DB.Exec(`UPDATE projects SET owner = 'someone else' WHERE id = ?`, id)
		ps.DB.Exec(`UPDATE chats SET updated_at = ? WHERE id = ?`, chatUpdated, chat)
	}}
	turn, err := s.Ask(ownerCtx, chat, "Add a related-work subsection", Context{}, Choice{}, func(agent.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	if turn.Status != "failed" || len(turn.Files) != 0 {
		t.Fatalf("turn = %+v", turn)
	}
	if file(t, ps, id, "main.tex") != mainTex || len(commits(t, ps, id)) != before {
		t.Error("the turn wrote to a project its user no longer owns")
	}
	var reply, reasoning, activity, status string
	ps.DB.QueryRow(`SELECT reply, reasoning, activity, status FROM turns WHERE id = ?`, turn.ID).Scan(&reply, &reasoning, &activity, &status)
	if reply != "" || reasoning != "" || activity != "[]" || status != "failed" {
		t.Errorf("stored turn: reply %q, reasoning %q, activity %s, status %s", reply, reasoning, activity, status)
	}
	var touched int64
	ps.DB.QueryRow(`SELECT updated_at FROM chats WHERE id = ?`, chat).Scan(&touched)
	if touched != chatUpdated {
		t.Error("the chat was touched after access was lost")
	}
}

// Stopping a turn, or a provider error, after access was lost keeps nothing
// either: every way a turn ends goes through the same check.
func TestTurnEndingsAfterAccessLossKeepNothing(t *testing.T) {
	for _, ending := range []string{"stopped", "provider error"} {
		t.Run(ending, func(t *testing.T) {
			ctx, cancel := context.WithCancel(ownerCtx)
			defer cancel()
			second := done("unused")
			if ending == "provider error" {
				second = []ai.Event{{Type: ai.EventError, Err: &ai.Error{Kind: ai.ErrOther, Message: "upstream failed"}}}
			}
			p := &fakeProvider{script: [][]ai.Event{
				{{Type: ai.EventReasoning, Text: "private reasoning"}, call("c1", "edit_file", addRelatedWork), {Type: ai.EventCompleted}},
				second,
			}}
			s, ps, id, chat := setup(t, p)
			p.before = map[int]func(){1: func() {
				ps.DB.Exec(`UPDATE projects SET owner = 'someone else' WHERE id = ?`, id)
				if ending == "stopped" {
					cancel()
				}
			}}
			turn, _ := s.Ask(ctx, chat, "Add a related-work subsection", Context{}, Choice{}, func(agent.Event) {})
			if turn == nil {
				t.Fatal("no turn")
			}
			var reply, reasoning, activity, status string
			ps.DB.QueryRow(`SELECT reply, reasoning, activity, status FROM turns WHERE id = ?`, turn.ID).Scan(&reply, &reasoning, &activity, &status)
			if reply != "" || reasoning != "" || activity != "[]" || status != "failed" {
				t.Errorf("stored turn: reply %q, reasoning %q, activity %s, status %s", reply, reasoning, activity, status)
			}
			if file(t, ps, id, "main.tex") != mainTex {
				t.Error("the turn changed the project")
			}
		})
	}
}
