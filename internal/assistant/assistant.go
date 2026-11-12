// Package assistant runs chats: each user message is a turn executed by the
// agent in its own git worktree, then reviewed (accept/reject) by the user.
package assistant

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/agent"
	"github.com/ponytojas/Refract/internal/ai"
	"github.com/ponytojas/Refract/internal/projects"
)

var (
	ErrNotFound = errors.New("not found")
	ErrBusy     = errors.New("the assistant is already working on this project")
	ErrState    = errors.New("this turn is not waiting for review")
)

// Chat is a conversation within a project.
type Chat struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"project_id"`
	Title     string    `json:"title"`
	Model     string    `json:"model"`  // last used, "" before the first turn
	Effort    string    `json:"effort"` //
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Turn is one request and what the assistant did about it.
type Turn struct {
	ID        string            `json:"id"`
	ChatID    string            `json:"chat_id"`
	Message   string            `json:"message"`
	Reply     string            `json:"reply"`
	Reasoning string            `json:"reasoning,omitempty"` // the model's reasoning summary, when it gives one
	Activity  []string          `json:"activity"`
	Status    string            `json:"status"` // running | review | done | accepted | rejected | failed | stopped
	Error     *ai.Error         `json:"error,omitempty"`
	Base      string            `json:"-"`
	Files     []projects.Change `json:"files"`   // changes still pending review
	Commits   []string          `json:"commits"` // created by accepting
	CreatedAt time.Time         `json:"created_at"`
}

// Context is what the editor attaches to a request.
type Context struct {
	OpenFile  string `json:"open_file"`
	Selection string `json:"selection"`
	FromLine  int    `json:"from_line"`
	ToLine    int    `json:"to_line"`
}

// Choice is the model and reasoning effort picked in the UI; empty fields mean the defaults.
type Choice struct {
	Model  string `json:"model"`
	Effort string `json:"effort"`
}

// Service owns chats and turns.
type Service struct {
	DB       *sql.DB
	Projects *projects.Service
	Agent    agent.Agent                                   // template; Model and Effort are filled by ModelFor
	ModelFor func(context.Context, Choice) (Choice, error) // resolves the user's choice for the agent loop
	Provider func(context.Context) ai.Provider             // the request user's provider; nil keeps Agent.Provider
	Access   *access.Checker                               // rechecks the user after a long turn, before writing

	mu        sync.Mutex
	running   map[string]bool // project id → a turn is running
	reviewing map[string]bool // turn id → an accept or reject is in progress
}

// review reserves a turn for one accept or reject at a time: both read the
// turn, act on the repository and save it, and two at once (a double click,
// two tabs) would save over each other.
func (s *Service) review(turnID string) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reviewing == nil {
		s.reviewing = map[string]bool{}
	}
	if s.reviewing[turnID] {
		return nil, ErrBusy
	}
	s.reviewing[turnID] = true
	return func() {
		s.mu.Lock()
		delete(s.reviewing, turnID)
		s.mu.Unlock()
	}, nil
}

const maxHistoryTurns = 10

// Recover marks turns left running by a restart as stopped and removes their worktrees.
// It runs at startup for every user, without one: it only ends turns, never writes work.
func (s *Service) Recover(ctx context.Context) error {
	rows, err := s.DB.QueryContext(ctx, `SELECT t.id, c.project_id FROM turns t JOIN chats c ON c.id = t.chat_id WHERE t.status = 'running'`)
	if err != nil {
		return err
	}
	type stale struct{ turn, project string }
	var list []stale
	for rows.Next() {
		var st stale
		if err := rows.Scan(&st.turn, &st.project); err != nil {
			rows.Close()
			return err
		}
		list = append(list, st)
	}
	rows.Close()
	for _, st := range list {
		s.Projects.EndTurn(ctx, st.project, st.turn)
		if _, err := s.DB.ExecContext(ctx, `UPDATE turns SET status = 'stopped', updated_at = ? WHERE id = ?`, time.Now().Unix(), st.turn); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) CreateChat(ctx context.Context, projectID string) (*Chat, error) {
	if _, err := s.Projects.Get(ctx, projectID); err != nil {
		return nil, err
	}
	now := time.Now()
	c := &Chat{ID: newID(), ProjectID: projectID, Title: "New chat", CreatedAt: now, UpdatedAt: now}
	_, err := s.DB.ExecContext(ctx, `INSERT INTO chats (id, project_id, title, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		c.ID, c.ProjectID, c.Title, now.Unix(), now.Unix())
	return c, err
}

func (s *Service) Chats(ctx context.Context, projectID string) ([]Chat, error) {
	if _, err := s.Projects.Get(ctx, projectID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT id, project_id, title, model, effort, created_at, updated_at FROM chats WHERE project_id = ? ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Chat{}
	for rows.Next() {
		var c Chat
		var created, updated int64
		if err := rows.Scan(&c.ID, &c.ProjectID, &c.Title, &c.Model, &c.Effort, &created, &updated); err != nil {
			return nil, err
		}
		c.CreatedAt, c.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Service) chat(ctx context.Context, id string) (*Chat, error) {
	var c Chat
	var created, updated int64
	err := s.DB.QueryRowContext(ctx, `SELECT id, project_id, title, model, effort, created_at, updated_at FROM chats WHERE id = ?`, id).
		Scan(&c.ID, &c.ProjectID, &c.Title, &c.Model, &c.Effort, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	// Whether the user may reach this chat was checked before (access.Require
	// on the chat's or turn's project, in the route table).
	c.CreatedAt, c.UpdatedAt = time.Unix(created, 0), time.Unix(updated, 0)
	return &c, nil
}

const turnCols = `id, chat_id, message, reply, reasoning, activity, status, error, base, files, commits, created_at`

func scanTurn(row interface{ Scan(...any) error }) (*Turn, error) {
	var t Turn
	var activity, errJSON, files, commits string
	var created int64
	if err := row.Scan(&t.ID, &t.ChatID, &t.Message, &t.Reply, &t.Reasoning, &activity, &t.Status, &errJSON, &t.Base, &files, &commits, &created); err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(activity), &t.Activity)
	json.Unmarshal([]byte(files), &t.Files)
	json.Unmarshal([]byte(commits), &t.Commits)
	if errJSON != "" {
		t.Error = &ai.Error{}
		json.Unmarshal([]byte(errJSON), t.Error)
	}
	t.CreatedAt = time.Unix(created, 0)
	return &t, nil
}

// Turns lists a chat's turns, oldest first.
func (s *Service) Turns(ctx context.Context, chatID string) ([]Turn, error) {
	if _, err := s.chat(ctx, chatID); err != nil {
		return nil, err
	}
	rows, err := s.DB.QueryContext(ctx, `SELECT `+turnCols+` FROM turns WHERE chat_id = ? ORDER BY created_at, rowid`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Turn{}
	for rows.Next() {
		t, err := scanTurn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (s *Service) turn(ctx context.Context, id string) (*Turn, *Chat, error) {
	t, err := scanTurn(s.DB.QueryRowContext(ctx, `SELECT `+turnCols+` FROM turns WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	c, err := s.chat(ctx, t.ChatID)
	return t, c, err
}

func (s *Service) save(ctx context.Context, t *Turn) error {
	activity, _ := json.Marshal(t.Activity)
	files, _ := json.Marshal(t.Files)
	commits, _ := json.Marshal(t.Commits)
	errJSON := ""
	if t.Error != nil {
		b, _ := json.Marshal(t.Error)
		errJSON = string(b)
	}
	now := time.Now().Unix()
	_, err := s.DB.ExecContext(ctx, `INSERT INTO turns (`+turnCols+`, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET reply = excluded.reply, reasoning = excluded.reasoning, activity = excluded.activity, status = excluded.status,
		error = excluded.error, base = excluded.base, files = excluded.files, commits = excluded.commits, updated_at = excluded.updated_at`,
		t.ID, t.ChatID, t.Message, t.Reply, t.Reasoning, string(activity), t.Status, errJSON, t.Base, string(files), string(commits), t.CreatedAt.Unix(), now)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE chats SET updated_at = ? WHERE id = ?`, now, t.ChatID)
	return err
}

// Ask runs a turn. emit streams agent events while it runs. Cancelling ctx
// (the user pressed Stop or left) stops the turn; nothing is applied.
func (s *Service) Ask(ctx context.Context, chatID, text string, ec Context, want Choice, emit func(agent.Event)) (*Turn, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, errors.New("empty message")
	}
	c, err := s.chat(ctx, chatID)
	if err != nil {
		return nil, err
	}
	p, err := s.Projects.Get(ctx, c.ProjectID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]bool{}
	}
	if s.running[p.ID] {
		s.mu.Unlock()
		return nil, ErrBusy
	}
	s.running[p.ID] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, p.ID)
		s.mu.Unlock()
	}()

	history, err := s.history(ctx, chatID)
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		s.DB.ExecContext(ctx, `UPDATE chats SET title = ? WHERE id = ?`, title(text), chatID)
	}
	t := &Turn{ID: newID(), ChatID: chatID, Message: withContext(text, ec), Status: "running", Activity: []string{}, Files: []projects.Change{}, Commits: []string{}, CreatedAt: time.Now()}
	if err := s.save(ctx, t); err != nil {
		return nil, err
	}
	// Whatever happens to the request, the turn's outcome is recorded.
	bg := context.WithoutCancel(ctx)
	fail := func(status string, err error) (*Turn, error) {
		// However the turn ends, what it produced is kept only for its user.
		if err := s.Access.Require(bg, p.ID, access.Owner); err != nil {
			return s.lost(bg, p.ID, t)
		}
		if ctx.Err() != nil {
			status = "stopped" // whatever failed, the user stopped it
		}
		t.Status = status
		var ae *ai.Error
		if !errors.As(err, &ae) {
			ae = &ai.Error{Kind: ai.ErrOther, Message: err.Error()}
		}
		if status == "failed" {
			t.Error = ae
		}
		s.Projects.EndTurn(bg, p.ID, t.ID) // nothing partial is kept
		t.Files = []projects.Change{}
		return t, s.save(bg, t)
	}

	choice, err := s.ModelFor(ctx, want)
	if err != nil {
		return fail("failed", err)
	}
	if _, err := s.DB.ExecContext(ctx, `UPDATE chats SET model = ?, effort = ? WHERE id = ?`, choice.Model, choice.Effort, chatID); err != nil {
		return fail("failed", err)
	}
	base, dir, err := s.Projects.StartTurn(ctx, p.ID, t.ID)
	if err != nil {
		return fail("failed", err)
	}
	t.Base = base

	a := s.Agent
	a.Model, a.Effort = choice.Model, choice.Effort
	if s.Provider != nil {
		a.Provider = s.Provider(ctx)
	}
	t.Reply, err = a.Run(ctx, agent.Input{Dir: dir, MainFile: p.MainFile, History: history, Message: t.Message}, func(e agent.Event) {
		switch e.Type {
		case "activity":
			t.Activity = append(t.Activity, e.Activity)
		case "reasoning":
			t.Reasoning += e.Text
		}
		emit(e)
	})
	switch {
	case ctx.Err() != nil:
		return fail("stopped", ctx.Err())
	case err != nil:
		return fail("failed", err)
	}

	// A turn runs for minutes: write its changes only if the user still owns
	// the project. Nothing changes owners today; a feature that does must not
	// do it while a turn runs (s.running), or this check and the write race.
	if err := s.Access.Require(bg, p.ID, access.Owner); err != nil {
		return s.lost(bg, p.ID, t)
	}
	files, err := s.Projects.FinishTurn(bg, p.ID, t.ID, base, "ai: "+title(text))
	if err != nil {
		return fail("failed", err)
	}
	t.Files = files
	t.Status = "review"
	if len(files) == 0 {
		t.Status = "done" // an answer without changes
		s.Projects.EndTurn(bg, p.ID, t.ID)
	}
	return t, s.save(bg, t)
}

// lost ends a turn whose user no longer has access to its project: nothing
// it produced is kept (changes, reply, reasoning, activity), and only the
// turn's own status is recorded, so it doesn't stay "running".
func (s *Service) lost(ctx context.Context, projectID string, t *Turn) (*Turn, error) {
	s.Projects.EndTurn(ctx, projectID, t.ID)
	t.Reply, t.Reasoning, t.Activity, t.Files = "", "", []string{}, []projects.Change{}
	t.Status = "failed"
	t.Error = &ai.Error{Kind: ai.ErrOther, Message: "you no longer have access to this project"}
	errJSON, _ := json.Marshal(t.Error)
	_, err := s.DB.ExecContext(ctx, `UPDATE turns SET reply = '', reasoning = '', activity = '[]', files = '[]', status = ?, error = ?, updated_at = ? WHERE id = ?`,
		t.Status, string(errJSON), time.Now().Unix(), t.ID)
	return t, err
}

// history replays earlier turns as plain messages (tool calls are not resent).
func (s *Service) history(ctx context.Context, chatID string) ([]agent.Message, error) {
	turns, err := s.Turns(ctx, chatID)
	if err != nil {
		return nil, err
	}
	turns = turns[max(0, len(turns)-maxHistoryTurns):]
	var out []agent.Message
	for _, t := range turns {
		if t.Status == "running" {
			continue
		}
		out = append(out, agent.Message{Role: "user", Text: t.Message})
		reply := t.Reply
		if reply == "" {
			reply = "(no answer: " + t.Status + ")"
		}
		switch t.Status {
		case "rejected":
			reply += "\n\n[The user rejected these changes.]"
		case "accepted":
			reply += "\n\n[The user accepted these changes.]"
		}
		out = append(out, agent.Message{Role: "assistant", Text: reply})
	}
	return out, nil
}

// FileDiff is one pending file's before/after content for the review view.
type FileDiff = projects.FileDiff

func (s *Service) Diff(ctx context.Context, turnID string) ([]FileDiff, error) {
	t, c, err := s.turn(ctx, turnID)
	if err != nil {
		return nil, err
	}
	out := []FileDiff{}
	for _, f := range t.Files {
		before, after, err := s.Projects.TurnFile(ctx, c.ProjectID, t.ID, t.Base, f.Path)
		if err != nil {
			return nil, err
		}
		out = append(out, projects.NewFileDiff(f, before, after))
	}
	return out, nil
}

// Accept applies files (all pending if empty). On conflict nothing is applied
// and the conflicting files are returned.
func (s *Service) Accept(ctx context.Context, turnID string, files []string) (*Turn, []string, error) {
	release, err := s.review(turnID)
	if err != nil {
		return nil, nil, err
	}
	defer release()
	t, c, err := s.turn(ctx, turnID)
	if err != nil {
		return nil, nil, err
	}
	if t.Status != "review" {
		return nil, nil, ErrState
	}
	pending := map[string]bool{}
	for _, f := range t.Files {
		pending[f.Path] = true
	}
	if len(files) == 0 {
		for _, f := range t.Files {
			files = append(files, f.Path)
		}
	}
	for _, f := range files {
		if !pending[f] {
			return nil, nil, fmt.Errorf("%s is not pending in this turn", f)
		}
	}
	commit, conflicts, err := s.Projects.AcceptTurn(ctx, c.ProjectID, t.ID, t.Base, files, "ai: "+title(firstLine(t.Message)))
	if err != nil || len(conflicts) > 0 {
		return t, conflicts, err
	}
	if commit != "" {
		t.Commits = append(t.Commits, commit)
	}
	t.Files = slices.DeleteFunc(t.Files, func(f projects.Change) bool { return slices.Contains(files, f.Path) })
	if len(t.Files) == 0 {
		t.Status = "accepted"
		if err := s.Projects.EndTurn(ctx, c.ProjectID, t.ID); err != nil {
			slog.Error("end turn", "turn", t.ID, "err", err)
		}
	}
	return t, nil, s.save(ctx, t)
}

// Reject discards the turn's pending changes.
func (s *Service) Reject(ctx context.Context, turnID string) (*Turn, error) {
	release, err := s.review(turnID)
	if err != nil {
		return nil, err
	}
	defer release()
	t, c, err := s.turn(ctx, turnID)
	if err != nil {
		return nil, err
	}
	if t.Status != "review" {
		return nil, ErrState
	}
	if err := s.Projects.EndTurn(ctx, c.ProjectID, t.ID); err != nil {
		return nil, err
	}
	t.Status, t.Files = "rejected", []projects.Change{}
	if len(t.Commits) > 0 {
		t.Status = "accepted" // some files were accepted before; the rest is dropped
	}
	return t, s.save(ctx, t)
}

// withContext attaches the open file and selection to the request.
func withContext(text string, ec Context) string {
	var b strings.Builder
	b.WriteString(text)
	if ec.OpenFile != "" {
		fmt.Fprintf(&b, "\n\n[Open file: %s]", ec.OpenFile)
	}
	if ec.Selection != "" {
		sel := ec.Selection
		if len(sel) > 20000 {
			sel = sel[:20000] + "\n… (selection truncated)"
		}
		fmt.Fprintf(&b, "\n[Selected text, lines %d-%d of %s:]\n%s", ec.FromLine, ec.ToLine, ec.OpenFile, sel)
	}
	return b.String()
}

func title(text string) string {
	t := firstLine(text)
	if utf8.RuneCountInString(t) > 60 {
		t = string([]rune(t)[:60]) + "…"
	}
	return t
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
