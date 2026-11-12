package projects

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/netguard"
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

	ErrPrivateRemote = errors.New("the remote's address is on a private network, which the hosted service can't reach")
)

// allowFileRemotes lets tests use local bare repositories.
var allowFileRemotes = false

// lookupIP resolves remote hosts; tests replace it.
var lookupIP = net.DefaultResolver.LookupNetIP

// pinRemote checks, with PublicRemotes, that the remote's host is a public
// address, and returns the git options that make git connect to exactly the
// addresses checked (no second lookup, so no DNS rebinding) and refuse
// redirects (they would lead to a host nobody checked). Without
// PublicRemotes it returns nothing.
func (s *Service) pinRemote(ctx context.Context, raw string) ([]string, error) {
	if !s.PublicRemotes {
		return nil, nil
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, ErrBadRemote
	}
	if allowFileRemotes && u.Scheme == "file" {
		return nil, nil
	}
	host := u.Hostname()
	if host == "" || strings.HasSuffix(host, ".") {
		return nil, ErrBadRemote // a trailing dot wouldn't match the pinned name
	}
	opts := []string{"-c", "http.followRedirects=false"}
	if a, err := netip.ParseAddr(host); err == nil {
		if !netguard.Allowed(a) {
			return nil, ErrPrivateRemote
		}
		return opts, nil
	}
	addrs, err := lookupIP(ctx, "ip", host)
	if err != nil || len(addrs) == 0 {
		return nil, fmt.Errorf("can't find the host %s", host)
	}
	var pinned []string
	for _, a := range addrs {
		a = a.Unmap().WithZone("")
		if !netguard.Allowed(a) {
			return nil, ErrPrivateRemote
		}
		p := a.String()
		if a.Is6() {
			p = "[" + p + "]"
		}
		if !slices.Contains(pinned, p) {
			pinned = append(pinned, p)
		}
	}
	port := cmp.Or(u.Port(), "443")
	return append(opts, "-c", "http.curloptResolve="+host+":"+port+":"+strings.Join(pinned, ",")), nil
}

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

// Remote returns the project's remote, or ErrNoRemote. A GitHub remote gets
// the token of the user acting, never of whoever owns the project now, and
// only for their own project: remote operations act with their credentials.
func (s *Service) Remote(ctx context.Context, id string) (*Remote, error) {
	u, err := access.UserFrom(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.Owner != u.ID {
		return nil, ErrNotFound
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
		if r.Token, err = s.GitHubToken(ctx, u.ID); err != nil {
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
	// An early, friendly error; every push and pull checks again. A host that
	// can't be found yet is saved anyway, as without PublicRemotes.
	if _, err := s.pinRemote(ctx, r.URL); errors.Is(err, ErrPrivateRemote) || errors.Is(err, ErrBadRemote) {
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
// Only https (and file, in tests) transports are allowed. With
// PublicRemotes, git connects only to the public addresses pinRemote checked,
// directly: a proxy would resolve the host itself.
func (s *Service) gitRemote(ctx context.Context, dir string, r *Remote, args ...string) (string, error) {
	pin, err := s.pinRemote(ctx, r.URL)
	if err != nil {
		return "", err
	}
	allow := append(pin, "-c", "protocol.allow=never", "-c", "protocol.https.allow=always")
	if allowFileRemotes {
		allow = append(allow, "-c", "protocol.file.allow=always")
	}
	var env []string
	if r.Token != "" {
		basic := base64.StdEncoding.EncodeToString([]byte("x-access-token:" + r.Token))
		env = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http.extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + basic}
	}
	if s.PublicRemotes {
		env = append(env, "NO_PROXY=*", "no_proxy=*")
	}
	out, err := gitEnv(ctx, dir, env, append(allow, args...)...)
	if err != nil && r.Token != "" {
		err = errors.New(strings.ReplaceAll(err.Error(), r.Token, "***")) // never echo the token
	}
	return out, err
}

// remoteLocks: one push or pull at a time per project. Push and Pull load the
// remote (who may act, with which credentials) while holding it, so anything
// that ever changes a project's owner must hold it too.
var remoteLocks sync.Map // project id → *sync.Mutex

func lockRemote(id string) func() {
	m, _ := remoteLocks.LoadOrStore(id, &sync.Mutex{})
	m.(*sync.Mutex).Lock()
	return m.(*sync.Mutex).Unlock
}

// Push saves pending edits as a commit and pushes the history to the remote branch.
func (s *Service) Push(ctx context.Context, id string) error {
	defer lockRemote(id)()
	r, err := s.Remote(ctx, id)
	if err != nil {
		return err
	}
	unlock := lockRepo(id) // only while committing: never across the network
	_, err = s.checkpoint(ctx, id, "Checkpoint before push", false)
	unlock()
	if err != nil {
		return err
	}
	_, err = s.gitRemote(ctx, s.Path(id), r, "push", "--porcelain", r.URL, "HEAD:refs/heads/"+r.Branch)
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
	defer lockRemote(id)()
	r, err := s.Remote(ctx, id)
	if err != nil {
		return nil, err
	}
	unlock := lockRepo(id) // released during the fetch, taken again for the merge
	_, err = s.checkpoint(ctx, id, "Checkpoint before pull", false)
	unlock()
	if err != nil {
		return nil, err
	}
	dir := s.Path(id)
	if _, err := s.gitRemote(ctx, dir, r, "fetch", "--no-tags", r.URL, "refs/heads/"+r.Branch); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref") {
			return &PullResult{Empty: true, Changed: []string{}, Conflicts: []string{}}, nil
		}
		return nil, err
	}
	defer lockRepo(id)()
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
