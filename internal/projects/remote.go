package projects

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/ponytojas/Refract/internal/store"
)

// Remote is where a project's history is pushed and pulled. Token is sent as
// HTTP basic auth (as GitHub, GitLab and Gitea accept personal access tokens);
// it is stored sealed and never returned to the client.
type Remote struct {
	URL      string `json:"url"`
	Branch   string `json:"branch"`
	AutoPush bool   `json:"auto_push"`
	GitHub   bool   `json:"github"` // authenticate with the connected GitHub account
	Token    string `json:"token,omitempty"`
}

var (
	ErrNoRemote  = errors.New("this project has no remote yet")
	ErrBadRemote = errors.New("the remote must be an https:// URL, without a user name or password in it (put the token in its own field)")
	ErrBehind    = errors.New("the remote has commits this project doesn't: pull first")
	branchName   = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)
)

// allowFileRemotes lets tests use local bare repositories.
var allowFileRemotes = false

func checkRemote(r Remote) error {
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" && !(allowFileRemotes && u.Scheme == "file") {
		return ErrBadRemote
	}
	if u.Scheme != "https" && !(allowFileRemotes && u.Scheme == "file") || u.User != nil || strings.ContainsAny(r.URL, " \n\t") {
		return ErrBadRemote
	}
	if !branchName.MatchString(r.Branch) || strings.Contains(r.Branch, "..") {
		return fmt.Errorf("%q is not a valid branch name", r.Branch)
	}
	return nil
}

func remoteKey(id string) string { return "remote:" + id }

// Remote returns the project's remote, or ErrNoRemote.
func (s *Service) Remote(ctx context.Context, id string) (*Remote, error) {
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	sealed, err := store.GetSetting(ctx, s.DB, remoteKey(id))
	if err != nil {
		return nil, err
	}
	if sealed == "" {
		return nil, ErrNoRemote
	}
	plain, err := store.Unseal(s.Key, sealed, remoteKey(id))
	if err != nil {
		return nil, err
	}
	var r Remote
	if err := json.Unmarshal(plain, &r); err != nil {
		return nil, err
	}
	if r.GitHub && s.GitHubToken != nil {
		if r.Token, err = s.GitHubToken(ctx, p.Owner); err != nil {
			return nil, err
		}
	}
	return &r, nil
}

// SetRemote saves the remote; an empty URL removes it. An empty token keeps
// the stored one (the client never sees it) unless keepToken is false.
func (s *Service) SetRemote(ctx context.Context, id string, r Remote, keepToken bool) error {
	if _, err := s.Get(ctx, id); err != nil {
		return err
	}
	if r.URL == "" {
		return store.SetSetting(ctx, s.DB, remoteKey(id), "")
	}
	r.URL = strings.TrimSpace(r.URL)
	if r.Branch == "" {
		r.Branch = "main"
	}
	if err := checkRemote(r); err != nil {
		return err
	}
	if r.GitHub {
		r.Token = "" // the account's token is looked up on use, never copied
	} else if r.Token == "" && keepToken {
		if old, err := s.Remote(ctx, id); err == nil && !old.GitHub {
			r.Token = old.Token
		}
	}
	plain, _ := json.Marshal(r)
	sealed, err := store.Seal(s.Key, plain, remoteKey(id))
	if err != nil {
		return err
	}
	return store.SetSetting(ctx, s.DB, remoteKey(id), sealed)
}

// gitRemote runs a network git command against r. The token travels in an
// environment-only config entry: never on the command line, never on disk.
// Only https (and file, in tests) transports are allowed.
func gitRemote(ctx context.Context, dir string, r *Remote, args ...string) (string, error) {
	allow := []string{"-c", "protocol.allow=never", "-c", "protocol.https.allow=always"}
	if allowFileRemotes {
		allow = append(allow, "-c", "protocol.file.allow=always")
	}
	var env []string
	if r.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + r.Token))
		env = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + basic}
	}
	out, err := gitEnv(ctx, dir, env, append(allow, args...)...)
	if err != nil && r.Token != "" {
		err = errors.New(strings.ReplaceAll(err.Error(), r.Token, "***")) // never echo the token
	}
	return out, err
}

var remoteLocks sync.Map // project id → *sync.Mutex: one push/pull at a time

func lockRemote(id string) func() {
	m, _ := remoteLocks.LoadOrStore(id, &sync.Mutex{})
	m.(*sync.Mutex).Lock()
	return m.(*sync.Mutex).Unlock
}

// Push saves pending edits as a commit and pushes the history to the remote branch.
func (s *Service) Push(ctx context.Context, id string) error {
	r, err := s.Remote(ctx, id)
	if err != nil {
		return err
	}
	defer lockRemote(id)()
	if _, err := s.checkpoint(ctx, id, "Checkpoint before push", false); err != nil {
		return err
	}
	_, err = gitRemote(ctx, s.Path(id), r, "push", "--porcelain", r.URL, "HEAD:refs/heads/"+r.Branch)
	if err != nil && (strings.Contains(err.Error(), "rejected") || strings.Contains(err.Error(), "non-fast-forward")) {
		return ErrBehind
	}
	return err
}

// PullResult says what a pull changed.
type PullResult struct {
	Changed   []string `json:"changed"`   // files that changed on disk
	Conflicts []string `json:"conflicts"` // set when nothing was merged
	Empty     bool     `json:"empty"`     // the remote branch doesn't exist yet
}

// Pull saves pending edits as a commit, fetches the remote branch and merges
// it: a fast-forward when possible, else a merge commit. On conflicts the
// merge is aborted and nothing changes.
func (s *Service) Pull(ctx context.Context, id string) (*PullResult, error) {
	r, err := s.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	defer lockRemote(id)()
	if _, err := s.checkpoint(ctx, id, "Checkpoint before pull", false); err != nil {
		return nil, err
	}
	dir := s.Path(id)
	if _, err := gitRemote(ctx, dir, r, "fetch", "--no-tags", r.URL, "refs/heads/"+r.Branch); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			return &PullResult{Empty: true, Changed: []string{}, Conflicts: []string{}}, nil
		}
		return nil, err
	}
	before, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	before = strings.TrimSpace(before)
	res := &PullResult{Changed: []string{}, Conflicts: []string{}}
	if _, err := git(ctx, dir, "merge", "-q", "--no-edit", "--allow-unrelated-histories", "-m", "Merge "+r.Branch+" from the remote", "FETCH_HEAD"); err != nil {
		out, _ := git(ctx, dir, "diff", "--name-only", "--diff-filter=U")
		git(ctx, dir, "merge", "--abort")
		if conflicts := strings.Fields(out); len(conflicts) > 0 {
			res.Conflicts = conflicts
			return res, nil
		}
		return nil, err
	}
	changes, err := diffNames(ctx, dir, before, "HEAD")
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		res.Changed = append(res.Changed, c.Path)
	}
	if len(res.Changed) > 0 {
		s.committed(ctx, id)
	}
	return res, s.touch(ctx, id)
}
