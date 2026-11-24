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
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/ponytojas/Refract/internal/access"
	"github.com/ponytojas/Refract/internal/netguard"
	"github.com/ponytojas/Refract/internal/store"
)

// Remote is where a project's history is pushed and pulled. Token is sent as
// HTTP basic auth (as GitHub, GitLab and Gitea accept personal access tokens);
// it is stored sealed and never returned to the client. A typed token belongs
// to the origin (scheme, host and port) it was saved for, TokenOrigin, and is
// sent nowhere else.
type Remote struct {
	URL         string `json:"url"`
	Branch      string `json:"branch"`
	AutoPush    bool   `json:"auto_push"`
	GitHub      bool   `json:"github"` // authenticate with the connected GitHub account
	Token       string `json:"token,omitempty"`
	TokenOrigin string `json:"token_origin,omitempty"`
}

var (
	ErrNoRemote  = errors.New("this project has no remote yet")
	ErrBadRemote = errors.New("the remote must be an https:// URL, without a user name or password in it (put the token in its own field)")
	ErrBehind    = errors.New("the remote has commits this project doesn't: pull first")
	branchName   = regexp.MustCompile(`^[A-Za-z0-9._-]+(/[A-Za-z0-9._-]+)*$`)

	ErrPrivateRemote   = errors.New("the remote's address is on a private network, which the hosted service can't reach")
	ErrLinkLocalRemote = errors.New("the remote's address is a link-local address (such as a cloud metadata service), which Refract never connects to")
	ErrTokenOrigin     = errors.New("the remote is on another host now, and the saved token only works with the old one: type an access token for the new host, or save without one")
	ErrGitHubRemote    = errors.New("a remote that uses the GitHub account must be a repository on GitHub")
)

// remoteOrigin is the scheme, host and port a remote URL connects to, in one
// form, as a browser's URL parser writes it: https://Example.com/x,
// https://example.com:0443/y and https://example.com:443/z are the same
// origin, and so are two spellings of one IPv6 address. It is empty for a URL
// that doesn't parse, or whose host checkRemote refuses (non-ASCII, zones).
func remoteOrigin(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "file" {
		return "file://"
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || !asciiHost(host) {
		return ""
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if a.Zone() != "" {
			return ""
		}
		host = a.String()
	}
	port := u.Port()
	if port != "" {
		n, err := strconv.ParseUint(port, 10, 16)
		if err != nil {
			return ""
		}
		port = strconv.FormatUint(n, 10)
	}
	if port == "" && scheme == "https" {
		port = "443"
	}
	return scheme + "://" + net.JoinHostPort(host, port)
}

// asciiHost: an international domain name must be written in its xn-- form,
// so that the server and the browser compare the same name.
func asciiHost(h string) bool {
	for _, c := range h {
		if c > 0x7e || c <= 0x20 {
			return false
		}
	}
	return true
}

func (s *Service) githubOrigin() string {
	return remoteOrigin(cmp.Or(s.GitHubOrigin, "https://github.com"))
}

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
		return nil, s.refuseLinkLocal(ctx, raw)
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

// refuseLinkLocal is what remotes get without PublicRemotes: link-local
// addresses (169.254.0.0/16, fe80::/10: cloud metadata services) are refused
// in every mode. An address written in the URL is checked, and so are the
// addresses a host name resolves to when the push or pull starts; unlike
// PublicRemotes, git isn't pinned to them, so this is a check, not a guarantee.
func (s *Service) refuseLinkLocal(ctx context.Context, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" {
		return nil // checkRemote and git judge the rest
	}
	host := u.Hostname()
	if a, err := netip.ParseAddr(host); err == nil {
		if isLinkLocal(a) {
			return ErrLinkLocalRemote
		}
		return nil
	}
	addrs, err := lookupIP(ctx, "ip", host)
	if err != nil {
		return nil // a host that can't be found yet is saved anyway
	}
	for _, a := range addrs {
		if isLinkLocal(a) {
			return ErrLinkLocalRemote
		}
	}
	return nil
}

func isLinkLocal(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	return a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast()
}

func checkRemote(r Remote) error {
	u, err := url.Parse(r.URL)
	if err != nil || u.Host == "" && !(allowFileRemotes && u.Scheme == "file") {
		return ErrBadRemote
	}
	if u.Scheme != "https" && !(allowFileRemotes && u.Scheme == "file") || u.User != nil || strings.ContainsAny(r.URL, " \n\t") {
		return ErrBadRemote
	}
	if u.Scheme == "https" && (remoteOrigin(r.URL) == "" || u.RawQuery != "" || u.Fragment != "") {
		return ErrBadRemote // a port out of range, an IPv6 zone, a non-ASCII host, a query
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
	switch {
	case r.GitHub:
		// The account's token goes to GitHub only; a remote saved before that
		// was checked gets none, and gitRemote refuses it.
		r.Token, r.TokenOrigin = "", ""
		if s.GitHubToken != nil && remoteOrigin(r.URL) == s.githubOrigin() {
			if r.Token, err = s.GitHubToken(ctx, u.ID); err != nil {
				return nil, err
			}
			r.TokenOrigin = s.githubOrigin()
		}
	case r.Token != "" && r.TokenOrigin == "":
		// Saved by a release that didn't record it: the token belongs to the
		// URL it was last saved with.
		r.TokenOrigin = remoteOrigin(r.URL)
	}
	return &r, nil
}

// SetRemote saves the remote; an empty URL removes it. An empty token keeps
// the stored one (the client never sees it) unless keepToken is false; on a
// different origin it returns ErrTokenOrigin instead, so a token never follows
// the URL to another host.
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
	if _, err := s.pinRemote(ctx, r.URL); errors.Is(err, ErrPrivateRemote) || errors.Is(err, ErrLinkLocalRemote) || errors.Is(err, ErrBadRemote) {
		return err
	}
	switch {
	case r.GitHub:
		if remoteOrigin(r.URL) != s.githubOrigin() {
			return ErrGitHubRemote
		}
		r.Token, r.TokenOrigin = "", "" // the account's token is looked up on use, never copied
	case r.Token != "":
		r.TokenOrigin = remoteOrigin(r.URL)
	default:
		// The stored token is kept only for the origin it was saved for: on
		// another host or port, it must be typed again or dropped.
		r.TokenOrigin = ""
		if old, err := s.Remote(ctx, id); keepToken && err == nil && !old.GitHub && old.Token != "" {
			if remoteOrigin(r.URL) != old.TokenOrigin {
				return ErrTokenOrigin
			}
			r.Token, r.TokenOrigin = old.Token, old.TokenOrigin
		}
	}
	plain, _ := json.Marshal(r)
	sealed, err := store.Seal(s.Key, plain, remoteKey(id))
	if err != nil {
		return err
	}
	return store.SetSetting(ctx, s.DB, remoteKey(id), sealed)
}

// remoteArgs returns the git options and environment for a network command
// against r, after checking its credential belongs to r's origin: a typed
// token to the origin it was saved for, the GitHub account's to GitHub.
// With a credential, git doesn't follow redirects, which would carry it to
// a host nobody chose.
func (s *Service) remoteArgs(r *Remote, pin []string) (opts, env []string, err error) {
	origin := remoteOrigin(r.URL)
	switch {
	case r.GitHub && origin != s.githubOrigin():
		return nil, nil, ErrGitHubRemote
	case r.Token != "" && (r.TokenOrigin == "" || origin != r.TokenOrigin):
		return nil, nil, ErrTokenOrigin
	}
	opts = append(slices.Clone(pin), "-c", "protocol.allow=never", "-c", "protocol.https.allow=always")
	if allowFileRemotes {
		opts = append(opts, "-c", "protocol.file.allow=always")
	}
	if r.Token != "" {
		// The header is set for the remote's origin only (git's http.<url>.*
		// matching), so a URL git rewrote, or redirected to, never gets it;
		// and redirects aren't followed at all, for any URL.
		scope := originURL(r.URL)
		for _, o := range []string{"http.followRedirects=false", "http." + scope + ".followRedirects=false"} {
			if !slices.Contains(opts, o) {
				opts = append(opts, "-c", o)
			}
		}
		env = []string{"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=http." + scope + ".extraHeader", "GIT_CONFIG_VALUE_0=Authorization: Basic " + basicAuth(r.Token)}
	}
	if s.PublicRemotes {
		env = append(env, "NO_PROXY=*", "no_proxy=*")
	}
	return opts, env, nil
}

// originURL is the root of r's origin as written in the URL (scheme://host[:port]/),
// the prefix git matches http.<url>.* settings against.
func originURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return u.Scheme + "://" + u.Host + "/"
}

func basicAuth(token string) string {
	return base64.StdEncoding.EncodeToString([]byte("x-access-token:" + token))
}

// redact removes a token from git's output, in clear and as the Basic
// credential it travels in.
func redact(msg, token string) string {
	if token == "" {
		return msg
	}
	return strings.NewReplacer(basicAuth(token), "***", token, "***").Replace(msg)
}

// networkEnv is the environment of git network commands: the server's,
// without anything that configures git (GIT_CONFIG_*, GIT_TRACE*, GIT_SSL_NO_VERIFY,
// GIT_PROXY_*…), which could route the request elsewhere or print the header.
// Only the locations of git's own programs and of extra CA certificates stay.
func networkEnv() []string {
	keep := []string{"GIT_EXEC_PATH", "GIT_SSL_CAINFO", "GIT_SSL_CAPATH"}
	var env []string
	for _, e := range os.Environ() {
		name, _, _ := strings.Cut(e, "=")
		if strings.HasPrefix(strings.ToUpper(name), "GIT_") && !slices.Contains(keep, name) {
			continue
		}
		env = append(env, e)
	}
	return append(env, "GIT_TRACE_REDACT=1")
}

// ErrRepoConfig: the project's git configuration has settings that change
// where or how git connects. Refract never writes them; refuse rather than guess.
var ErrRepoConfig = errors.New("the project's git configuration has url, http, include or credential settings, which Refract doesn't allow for remotes")

// checkRepoConfig reads the configuration the network command will see — with
// its environment, so every file git reads (.git/config, .git/config.worktree
// with extensions.worktreeConfig, anything included) — and refuses routing
// settings from anywhere but Refract's own command-line values.
func checkRepoConfig(ctx context.Context, dir string) error {
	out, err := runGit(ctx, dir, networkEnv(), nil, "config", "--list", "--includes", "--show-scope", "--name-only")
	if err != nil {
		return err
	}
	for _, line := range strings.Split(strings.ToLower(out), "\n") {
		scope, name, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok || scope == "command" {
			continue
		}
		for _, p := range []string{"url.", "http.", "include.", "includeif.", "credential."} {
			if strings.HasPrefix(name, p) {
				return ErrRepoConfig
			}
		}
	}
	return nil
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
	opts, env, err := s.remoteArgs(r, pin)
	if err != nil {
		return "", err
	}
	if err := checkRepoConfig(ctx, dir); err != nil {
		return "", err
	}
	out, err := runGit(ctx, dir, networkEnv(), env, append(opts, args...)...)
	if err != nil && r.Token != "" {
		err = errors.New(redact(err.Error(), r.Token)) // never echo the token
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
	return s.mergeFetched(ctx, id, r.Branch)
}

// mergeFetched merges FETCH_HEAD into the project, under lockRepo. Edits saved
// while the fetch ran are committed first: git refuses to merge over them, and
// committed they merge, or show up as conflicts, like any other.
func (s *Service) mergeFetched(ctx context.Context, id, branch string) (*PullResult, error) {
	if _, err := s.checkpoint(ctx, id, "Checkpoint before pull", false); err != nil {
		return nil, err
	}
	dir := s.Path(id)
	before, err := git(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	before = strings.TrimSpace(before)
	res := &PullResult{Changed: []string{}, Conflicts: []string{}}
	if _, err := git(ctx, dir, "merge", "-q", "--no-edit", "--allow-unrelated-histories", "-m", "Merge "+branch+" from the remote", "FETCH_HEAD"); err != nil {
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
