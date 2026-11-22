package compile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func events(t *testing.T, stderr string) []Event {
	t.Helper()
	var got []Event
	w := &progressWriter{report: func(e Event) { got = append(got, e) }}
	// Write in odd pieces: lines must be rebuilt across writes.
	for len(stderr) > 0 {
		n := min(len(stderr), 37)
		w.Write([]byte(stderr[:n]))
		stderr = stderr[n:]
	}
	w.close()
	return got
}

func TestProgressDownloadFixture(t *testing.T) {
	got := events(t, testdata(t, "download_ok.stderr"))
	if len(got) != 14 {
		t.Fatalf("%d events: %+v", len(got), got)
	}
	if got[0] != (Event{Kind: Downloading, File: "tectonic-format-latex.tex"}) {
		t.Errorf("first = %+v", got[0])
	}
	for _, e := range got {
		if e.Kind != Downloading {
			t.Errorf("unexpected %+v", e)
		}
	}
}

func TestProgressOfflineFixture(t *testing.T) {
	got := events(t, testdata(t, "download_offline.stderr"))
	want := []Event{
		{Kind: Downloading, File: "siunitx.sty"},
		{Kind: FetchFailed, File: "siunitx.sty", Attempt: 1, Network: true},
		{Kind: FetchFailed, File: "siunitx.sty", Attempt: 2, Network: true},
		{Kind: FetchFailed, File: "siunitx.sty", Attempt: 3, Network: true},
		{Kind: DownloadFailed, File: "siunitx.sty"},
		{Kind: Downloading, File: "siunitx.sty"},
	}
	if len(got) < len(want) || !reflect.DeepEqual(got[:len(want)], want) {
		t.Fatalf("got %+v", got)
	}
}

func TestProgressNetworkCauses(t *testing.T) {
	for cause, network := range map[string]bool{
		"dns error: failed to lookup address information": true,
		"operation timed out":                             true,
		"Connection refused (os error 61)":                true,
		"client error (Connect)":                          true,
		"unexpected status 404 Not Found":                 false,
	} {
		out := "warning: failure fetching \"a.sty\" from network (1/3)\ncaused by: error sending request\ncaused by: " + cause + "\nnote: Running TeX ...\n"
		got := events(t, out)
		if len(got) != 1 || got[0].Network != network || got[0].Attempt != 1 {
			t.Errorf("%q: %+v, want network=%v once", cause, got, network)
		}
	}
}

func fakeTectonic(t *testing.T, body string) (bin, pidFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs sh")
	}
	dir := t.TempDir()
	pidFile = filepath.Join(dir, "pid")
	bin = filepath.Join(dir, "tectonic")
	script := "#!/bin/sh\necho $$ > '" + pidFile + "'\n" + body
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, pidFile
}

func TestEarlyStopOnNetworkFailure(t *testing.T) {
	fixture, _ := filepath.Abs("testdata/download_offline.stderr")
	bin, pidFile := fakeTectonic(t, "head -9 '"+fixture+"' >&2\nsleep 30\nexit 0\n")
	dir := t.TempDir()
	write(t, dir, "main.tex", "x")
	// A PDF from a previous good build must stay.
	write(t, dir, BuildDir+"/main.pdf", "%PDF old")
	tc := &Tectonic{Bin: bin, Timeout: time.Minute}
	var seen []Event
	ctx := WithReporter(context.Background(), func(e Event) { seen = append(seen, e) })
	start := time.Now()
	res, err := tc.Compile(ctx, dir, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatalf("took %s: the run was not stopped", time.Since(start))
	}
	if !res.Downloads.Offline || res.PDF != "" || res.OK || res.TimedOut {
		t.Fatalf("res = %+v", res)
	}
	if !reflect.DeepEqual(res.Downloads.Missing, []string{"siunitx.sty"}) {
		t.Errorf("missing = %v", res.Downloads.Missing)
	}
	var dl *Problem
	for i, p := range res.Problems {
		if p.Kind == "download" {
			dl = &res.Problems[i]
		}
		if strings.Contains(p.Message, "failure fetching") {
			t.Errorf("cascading noise kept: %+v", p)
		}
	}
	if dl == nil || dl.Severity != "error" || !strings.Contains(dl.Message, "network problem, not an error in your document") || !strings.Contains(dl.Message, "siunitx.sty") {
		t.Errorf("problems = %+v", res.Problems)
	}
	if len(seen) == 0 {
		t.Error("the context reporter saw nothing")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, BuildDir, "main.pdf")); string(b) != "%PDF old" {
		t.Errorf("previous PDF lost: %q", b)
	}
	b, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid == 0 {
		t.Fatal("the fake tectonic did not start")
	}
	time.Sleep(100 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err == nil {
		t.Error("tectonic is still running")
	}
}

func TestTimeoutWhileDownloadingSaysCompileAgain(t *testing.T) {
	bin, _ := fakeTectonic(t, "echo 'note: downloading foo.sty'\nsleep 30\n") // Tectonic prints notes on stdout
	dir := t.TempDir()
	write(t, dir, "main.tex", "x")
	tc := &Tectonic{Bin: bin, Timeout: 1500 * time.Millisecond}
	res, err := tc.Compile(context.Background(), dir, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || !res.Downloads.Interrupted || res.Downloads.Fetched != 1 || res.Downloads.Offline {
		t.Fatalf("res = %+v", res)
	}
	msg := res.Problems[0].Message
	if res.Problems[0].Kind != "download" || !strings.Contains(msg, "Compile again to continue") || strings.Contains(msg, "infinite loop") {
		t.Errorf("message = %q", msg)
	}
}

func TestTimeoutLongAfterADownloadIsALoop(t *testing.T) {
	defer func(q time.Duration) { downloadQuiet = q }(downloadQuiet)
	downloadQuiet = 300 * time.Millisecond
	bin, _ := fakeTectonic(t, "echo 'note: downloading foo.sty'\nsleep 30\n")
	dir := t.TempDir()
	write(t, dir, "main.tex", "x")
	res, err := (&Tectonic{Bin: bin, Timeout: 1500 * time.Millisecond}).Compile(context.Background(), dir, "main.tex")
	if err != nil {
		t.Fatal(err)
	}
	if !res.TimedOut || res.Downloads.Interrupted || !strings.Contains(res.Problems[0].Message, "infinite loop") {
		t.Fatalf("res = %+v", res)
	}
}

func TestDownloadsJSONHasAList(t *testing.T) {
	b, err := json.Marshal(&Result{Superseded: true})
	if err != nil || !strings.Contains(string(b), `"missing":[]`) {
		t.Fatalf("%s %v", b, err)
	}
}

func TestRunnerActivity(t *testing.T) {
	bin, _ := fakeTectonic(t, "echo 'note: downloading a.sty'\necho 'note: downloading b.sty'\nsleep 30\n")
	dir := t.TempDir()
	write(t, dir, "main.tex", "x")
	r := &Runner{Compiler: &Tectonic{Bin: bin, Timeout: time.Minute}}
	if got := r.Activity("p"); got.State != "idle" {
		t.Fatalf("before: %+v", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Compile(ctx, "p", dir, "main.tex"); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	var got Activity
	for time.Now().Before(deadline) {
		if got = r.Activity("p"); got.Downloaded == 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got.State != "downloading" || got.File != "b.sty" || got.Downloaded != 2 {
		t.Fatalf("during: %+v", got)
	}
	cancel()
	<-done
	if got := r.Activity("p"); got.State != "idle" {
		t.Fatalf("after: %+v", got)
	}
}
