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
	// FetchFailed: one attempt (Attempt of Total) to fetch File failed. Network
	// means the server could not be reached at all (no DNS, no route,
	// refused or timed out).
	FetchFailed
	// DownloadFailed: Tectonic gave up on File.
	DownloadFailed
	// Running: Tectonic started a stage (File is "TeX", "xdvipdfmx"…).
	Running
	// Started: the compile left the queue and began (sent by Queue).
	Started
	// BundleUnreachable: the bundle isn't cached and its index can't be
	// fetched, so nothing can be compiled (a first compile offline).
	BundleUnreachable
)

// Event is one thing Tectonic said about downloads, read from its output.
type Event struct {
	Kind    EventKind
	File    string
	Attempt int  // FetchFailed
	Total   int  // FetchFailed: how many attempts Tectonic makes
	Network bool // FetchFailed: the server could not be reached
}

// Reporter receives events while a compile runs, in the order Tectonic wrote
// them. Calls never overlap (the compile serializes them) but may come from
// different goroutines. It must return quickly.
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
	reFetchFailed = regexp.MustCompile(`^warning: failure fetching "(.+)" from network \((\d+)/(\d+)\)$`)
	reRunning     = regexp.MustCompile(`^note: Running (\S+)`)
	reGaveUp      = regexp.MustCompile(`^caused by: failed to download "(.+)"; please check your network connection\.$`)
	// Tectonic 0.17 with an empty cache and no network fails before any
	// download is announced; it says so as an error, or in a panic.
	reNoBundle = regexp.MustCompile(`this bundle isn't cached, and we couldn't get it from the internet`)
	// What Tectonic 0.17 (reqwest) says when there is no connection.
	networkCauses = []string{"dns error", "client error (connect)", "operation timed out", "connection refused", "timed out"}
)

// progressWriter splits what Tectonic writes (stdout and stderr) into lines and
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
		total, _ := strconv.Atoi(m[3])
		w.pending = &Event{Kind: FetchFailed, File: m[1], Attempt: n, Total: total}
	case reRunning.MatchString(l):
		w.emit(Event{Kind: Running, File: reRunning.FindStringSubmatch(l)[1]})
	case reNoBundle.MatchString(l):
		w.emit(Event{Kind: BundleUnreachable})
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
	finishing   bool      // Tectonic is writing the PDF (xdvipdfmx): what it still fetches is optional
	lastWasDown bool      // the last event was the start of a download
	lastFile    string    // that download's file
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
		d.lastWasDown, d.lastFile, d.lastAt = true, e.File, now
	case FetchFailed:
		d.lastWasDown = false
		if e.Network && e.Attempt >= e.Total {
			d.fail(e.File) // earlier attempts are retried by Tectonic
		}
	case DownloadFailed:
		d.lastWasDown = false
		d.fail(e.File)
	case Running:
		d.lastWasDown = false
		d.finishing = e.File == "xdvipdfmx"
	case BundleUnreachable:
		d.lastWasDown = false
		d.offline = true
	}
}

// inProgress is the file whose download was cut off, if the run ended now.
func (d *downloadState) inProgress() string {
	if d.lastWasDown {
		return d.lastFile
	}
	return ""
}

// downloadingAt reports whether the run was most likely still downloading at now.
func (d *downloadState) downloadingAt(now time.Time) bool {
	return d.lastWasDown && now.Sub(d.lastAt) < downloadQuiet
}

func (d *downloadState) fail(file string) {
	if !d.finishing { // after xdvipdfmx only optional files (maps) are fetched: the PDF still comes
		d.offline = true
	}
	for _, m := range d.missing {
		if m == file {
			return
		}
	}
	d.missing = append(d.missing, file)
}

// stops: no point letting TeX go on once Tectonic gave up on a file, or the
// server stayed unreachable through the last attempt: the PDF would only be
// broken. Earlier attempts are Tectonic's to retry.
func (e Event) stops() bool {
	return e.Kind == DownloadFailed || (e.Kind == FetchFailed && e.Network && e.Attempt >= e.Total)
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

// reCascade matches the problems that follow from a failed download.
var reCascade = regexp.MustCompile("^(failure fetching|open of input)|failed to download|this bundle isn't cached")

// reNoFile matches "file not found" problems, which name the file.
var reNoFile = regexp.MustCompile("^(File `.*' not found|I can't find file)")

// dropDownloadNoise removes the problems that only repeat a failed download.
// A "file not found" for any other file is the person's own and stays.
func dropDownloadNoise(ps []Problem, missing []string) []Problem {
	out := ps[:0]
	for _, p := range ps {
		if reCascade.MatchString(p.Message) {
			continue
		}
		if reNoFile.MatchString(p.Message) && namesAny(p.Message, missing) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func namesAny(msg string, files []string) bool {
	for _, f := range files {
		if strings.Contains(msg, "`"+f+"'") || strings.Contains(msg, "`"+f+".") {
			return true
		}
	}
	return false
}

// Activity is what a project's compile is doing right now, for the status chip.
type Activity struct {
	State      string `json:"state"`      // idle | queued | compiling | downloading
	File       string `json:"file"`       // the file being downloaded
	Downloaded int    `json:"downloaded"` // files fetched so far by this compile
	Retrying   bool   `json:"retrying"`   // the last fetch failed and Tectonic is trying again
}

// Activity returns what key's compile is doing; idle when none runs. A
// download that started longer than downloadQuiet ago is over: TeX is
// compiling again (Tectonic announces downloads, not their end), so the chip
// doesn't keep naming the last file for the rest of a long compile.
func (r *Runner) Activity(key string) Activity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if a := r.act[key]; a != nil {
		act := a.Activity
		if act.State == "downloading" && time.Since(a.lastDownload) >= downloadQuiet {
			act.State, act.File = "compiling", ""
		}
		// Tectonic doesn't say when a retry succeeded: a quiet spell ends it.
		if act.Retrying && time.Since(a.lastRetry) >= downloadQuiet {
			act.Retrying = false
		}
		return act
	}
	return Activity{State: "idle"}
}

// liveActivity is the activity of one compile; seen counts its distinct downloads.
type liveActivity struct {
	Activity
	seen         map[string]bool
	lastDownload time.Time
	lastRetry    time.Time
}

func (r *Runner) startActivity(key, state string) *liveActivity {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.act == nil {
		r.act = map[string]*liveActivity{}
	}
	a := &liveActivity{Activity: Activity{State: state}, seen: map[string]bool{}}
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
	case Started:
		if a.State == "queued" {
			a.State = "compiling"
		}
	case Downloading:
		a.seen[e.File] = true
		a.State, a.File, a.Downloaded, a.Retrying = "downloading", e.File, len(a.seen), false
		a.lastDownload = time.Now()
	case FetchFailed:
		// Tectonic tries again unless this was the last attempt.
		a.Retrying = e.Attempt < e.Total
		a.lastRetry = time.Now()
	case DownloadFailed, Running, BundleUnreachable:
		a.Retrying = false
	}
}
