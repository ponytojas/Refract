package compile

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// EventKind tells what Tectonic reported about fetching a bundle file.
type EventKind int

const (
	// Downloading: Tectonic started fetching File from the package server.
	Downloading EventKind = iota + 1
	// FetchFailed: one attempt (Attempt of 3) to fetch File failed. Network
	// means the server could not be reached at all (no DNS, no route,
	// refused or timed out), so retrying now will not help.
	FetchFailed
	// DownloadFailed: Tectonic gave up on File.
	DownloadFailed
)

// Event is one thing Tectonic said about downloads, read from its stderr.
type Event struct {
	Kind    EventKind
	File    string
	Attempt int  // FetchFailed
	Network bool // FetchFailed: the server could not be reached
}

// Reporter receives events while a compile runs. It is called from the
// goroutine copying Tectonic's stderr and must return quickly.
type Reporter func(Event)

type reporterKey struct{}

// WithReporter returns a context whose compiles report download events to fn.
// A reporter already in ctx keeps receiving them too.
func WithReporter(ctx context.Context, fn Reporter) context.Context {
	if prev, ok := ctx.Value(reporterKey{}).(Reporter); ok {
		inner := fn
		fn = func(e Event) { prev(e); inner(e) }
	}
	return context.WithValue(ctx, reporterKey{}, fn)
}

func reporterFrom(ctx context.Context) Reporter {
	fn, _ := ctx.Value(reporterKey{}).(Reporter)
	return fn
}

var (
	reDownloading = regexp.MustCompile(`^note: downloading (\S.*)$`)
	reFetchFailed = regexp.MustCompile(`^warning: failure fetching "(.+)" from network \((\d+)/\d+\)$`)
	reGaveUp      = regexp.MustCompile(`^caused by: failed to download "(.+)"; please check your network connection\.$`)
	// What Tectonic 0.17 (reqwest) says when there is no connection.
	networkCauses = []string{"dns error", "client error (connect)", "operation timed out", "connection refused", "timed out"}
)

// progressWriter splits what Tectonic writes to stderr into lines and
// reports the download events among them. It keeps nothing: wrap it with
// a buffer for the output.
type progressWriter struct {
	report  Reporter
	partial []byte
	pending *Event // a "failure fetching" line whose causes are still being read
	sent    bool   // pending was already reported as a network failure
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.line(strings.TrimRight(string(w.partial[:i]), "\r"))
		w.partial = w.partial[i+1:]
	}
	if len(w.partial) > 64<<10 { // a line that never ends is not Tectonic's
		w.partial = nil
	}
	return len(p), nil
}

func (w *progressWriter) line(l string) {
	if w.pending != nil && strings.HasPrefix(l, "caused by:") && !w.sent {
		low := strings.ToLower(l)
		for _, c := range networkCauses {
			if strings.Contains(low, c) {
				w.pending.Network = true
				w.sent = true
				w.emit(*w.pending)
				return
			}
		}
	}
	if strings.HasPrefix(l, "caused by:") && w.pending != nil {
		// A cause of the pending failure; the "gave up" line below is its own event.
		if !reGaveUp.MatchString(l) {
			return
		}
	}
	w.settle()
	switch {
	case reGaveUp.MatchString(l):
		w.emit(Event{Kind: DownloadFailed, File: reGaveUp.FindStringSubmatch(l)[1]})
	case reDownloading.MatchString(l):
		w.emit(Event{Kind: Downloading, File: reDownloading.FindStringSubmatch(l)[1]})
	case reFetchFailed.MatchString(l):
		m := reFetchFailed.FindStringSubmatch(l)
		n, _ := strconv.Atoi(m[2])
		w.pending = &Event{Kind: FetchFailed, File: m[1], Attempt: n}
	}
}

// settle reports a pending failure that turned out not to be a network one.
func (w *progressWriter) settle() {
	if w.pending != nil && !w.sent {
		w.emit(*w.pending)
	}
	w.pending, w.sent = nil, false
}

func (w *progressWriter) emit(e Event) {
	if w.report != nil {
		w.report(e)
	}
}

// close reports what is still pending when the output ends.
func (w *progressWriter) close() {
	if len(w.partial) > 0 {
		w.line(strings.TrimRight(string(w.partial), "\r"))
		w.partial = nil
	}
	w.settle()
}

// downloadState tallies the events of one compile.
type downloadState struct {
	fetched     map[string]bool
	missing     []string
	offline     bool      // the package server could not be reached, or Tectonic gave up on a file
	lastWasDown bool      // the last event was the start of a download
	lastAt      time.Time // when that download started
}

// downloadQuiet is how long a single download may plausibly take: a run that
// timed out longer than this after its last download started was busy with
// something else (a loop in the document, say), not downloading.
var downloadQuiet = 15 * time.Second

func (d *downloadState) add(e Event, now time.Time) {
	switch e.Kind {
	case Downloading:
		if d.fetched == nil {
			d.fetched = map[string]bool{}
		}
		d.fetched[e.File] = true
		d.lastWasDown, d.lastAt = true, now
	case FetchFailed:
		d.lastWasDown = false
		if e.Network {
			d.fail(e.File)
		}
	case DownloadFailed:
		d.lastWasDown = false
		d.fail(e.File)
	}
}

// downloadingAt reports whether the run was most likely still downloading at now.
func (d *downloadState) downloadingAt(now time.Time) bool {
	return d.lastWasDown && now.Sub(d.lastAt) < downloadQuiet
}

func (d *downloadState) fail(file string) {
	d.offline = true
	for _, m := range d.missing {
		if m == file {
			return
		}
	}
	d.missing = append(d.missing, file)
}

// stops: no point letting TeX go on once the server is known to be unreachable
// or a file is known to be missing: the PDF would only be broken.
func (e Event) stops() bool {
	return e.Kind == DownloadFailed || (e.Kind == FetchFailed && e.Network)
}

// Downloads tells what a compile fetched from the package server.
type Downloads struct {
	Fetched     int      `json:"fetched"`     // files downloaded and kept in the cache
	Missing     []string `json:"missing"`     // files that could not be downloaded
	Offline     bool     `json:"offline"`     // the compile stopped because the server can't be reached
	Interrupted bool     `json:"interrupted"` // the time ran out while downloading
}

// MarshalJSON keeps missing a list, also in results built before any compile ran.
func (d Downloads) MarshalJSON() ([]byte, error) {
	type plain Downloads
	if d.Missing == nil {
		d.Missing = []string{}
	}
	return json.Marshal(plain(d))
}

// cascade matches the problems that follow from a missing download.
var reCascade = regexp.MustCompile("^(failure fetching|open of input|File `.*' not found|I can't find file)|failed to download")

func dropDownloadNoise(ps []Problem) []Problem {
	out := ps[:0]
	for _, p := range ps {
		if !reCascade.MatchString(p.Message) {
			out = append(out, p)
		}
	}
	return out
}

// Activity is what a project's compile is doing right now, for the status chip.
type Activity struct {
	State      string `json:"state"`      // idle | compiling | downloading
	File       string `json:"file"`       // the file being downloaded
	Downloaded int    `json:"downloaded"` // files fetched so far by this compile
	Retrying   bool   `json:"retrying"`   // the last fetch failed and Tectonic is trying again
}

// Activity returns what key's compile is doing; idle when none runs.
func (r *Runner) Activity(key string) Activity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a := r.act[key]; a != nil {
		return a.Activity
	}
	return Activity{State: "idle"}
}

// liveActivity is the activity of one compile; seen counts its distinct downloads.
type liveActivity struct {
	Activity
	seen map[string]bool
}

func (r *Runner) startActivity(key string) *liveActivity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.act == nil {
		r.act = map[string]*liveActivity{}
	}
	a := &liveActivity{Activity: Activity{State: "compiling"}, seen: map[string]bool{}}
	r.act[key] = a
	return a
}

func (r *Runner) endActivity(key string, a *liveActivity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.act[key] == a {
		delete(r.act, key)
	}
}

func (r *Runner) onEvent(a *liveActivity, e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch e.Kind {
	case Downloading:
		a.seen[e.File] = true
		a.State, a.File, a.Downloaded, a.Retrying = "downloading", e.File, len(a.seen), false
	case FetchFailed:
		a.Retrying = !e.Network
	}
}
