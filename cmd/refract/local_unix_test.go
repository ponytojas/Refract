//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A pipe where the key should be is refused instead of blocking startup.
func TestLoadOrCreateKeyFIFO(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), localKeyFile)
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan error, 1)
	go func() { _, err := loadOrCreateKey(fifo); done <- err }()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "regular file") {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a fifo as the key blocked")
	}
}

func TestReadEmbedTokenFromFD(t *testing.T) {
	r, w, _ := os.Pipe()
	w.WriteString(testEmbedToken + "\n")
	w.Close()
	t.Setenv(embedTokenEnv, "ignored-ignored-ignored")
	fd, err := syscall.Dup(int(r.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	tok, err := readEmbedToken(context.Background(), fd, os.Getenv, os.Unsetenv)
	if err != nil || tok != testEmbedToken {
		t.Fatal(tok, err)
	}
}

// A launcher that keeps its end of the token pipe open doesn't block
// startup: the read gives up after embedTokenWait, or as soon as ctx ends.
func TestReadEmbedTokenGivesUp(t *testing.T) {
	defer func(d time.Duration) { embedTokenWait = d }(embedTokenWait)
	for _, tc := range []struct {
		name, want string
		wait       time.Duration
		cancel     bool
	}{
		{"timeout", "after", 200 * time.Millisecond, false},
		{"cancelled", "stopped", time.Minute, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			embedTokenWait = tc.wait
			r, w, _ := os.Pipe()
			defer w.Close() // open: the token never ends
			fd, err := syscall.Dup(int(r.Fd()))
			r.Close()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if tc.cancel {
				time.AfterFunc(100*time.Millisecond, cancel)
			}
			defer cancel()
			errc := make(chan error, 1)
			go func() {
				_, err := readEmbedToken(ctx, fd, func(string) string { return "" }, func(string) error { return nil })
				errc <- err
			}()
			select {
			case err := <-errc:
				if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("err = %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("still waiting for the token")
			}
		})
	}
}

// SIGTERM, or the parent closing standard input, stops a child that is still
// waiting for the token, with the error line.
func TestEmbeddedStopsWhileWaitingForTheToken(t *testing.T) {
	for _, how := range []string{"sigterm", "stdin"} {
		t.Run(how, func(t *testing.T) {
			r, w, _ := os.Pipe()
			defer w.Close() // never written: the launcher is stuck
			exe, _ := os.Executable()
			cmd := exec.Command(exe, "--embedded", "--token-fd", "3")
			cmd.Env = localEnv(filepath.Join(t.TempDir(), "Refract"))
			cmd.ExtraFiles = []*os.File{r}
			var out bytes.Buffer
			cmd.Stdout = &out
			stdin, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			r.Close()
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			defer func() { cmd.Process.Kill(); <-done }()
			time.Sleep(300 * time.Millisecond) // let it reach the read
			if how == "sigterm" {
				cmd.Process.Signal(syscall.SIGTERM)
			} else {
				stdin.Close()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("still waiting for the token")
			}
			var j errorStatus
			if err := json.Unmarshal(out.Bytes(), &j); err != nil || j.State != stateError || !strings.Contains(j.Error, "waiting for the token") {
				t.Errorf("stdout %q (%v)", out.String(), err)
			}
		})
	}
}

func TestReadEmbedTokenRefusesStandardDescriptors(t *testing.T) {
	for fd := 0; fd <= 2; fd++ {
		if _, err := readEmbedToken(context.Background(), fd, func(string) string { return "" }, func(string) error { return nil }); err == nil || !strings.Contains(err.Error(), "3 or more") {
			t.Errorf("--token-fd %d: %v", fd, err)
		}
	}
}

// Closing standard input lets a request in flight (a compile, here with a slow
// fake tectonic) finish and answer before the process exits.
func TestEmbeddedStdinCloseFinishesRequestsInFlight(t *testing.T) {
	bin := t.TempDir()
	started := filepath.Join(bin, "started")
	script := "#!/bin/sh\ntouch '" + started + "'\nsleep 2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "tectonic"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := startEmbedded(t, filepath.Join(t.TempDir(), "Refract"), stateReady, "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	call := func(method, path, body string) (*http.Response, []byte, error) {
		req, _ := http.NewRequest(method, c.line.URL+path, strings.NewReader(body))
		req.Header.Set("X-Refract", "1")
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: embedCookie, Value: testEmbedToken})
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp, b, err
	}
	resp, b, err := call("POST", "/api/projects", `{"name":"Drain","template":"article"}`)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("create: %v %v %s", resp, err, b)
	}
	var p struct {
		ID string `json:"id"`
	}
	json.Unmarshal(b, &p)

	type result struct {
		status int
		body   []byte
		err    error
	}
	done := make(chan result, 1)
	go func() {
		resp, b, err := call("POST", "/api/projects/"+p.ID+"/compile", "")
		r := result{body: b, err: err}
		if resp != nil {
			r.status = resp.StatusCode
		}
		done <- r
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if _, err := os.Stat(started); err == nil {
			break
		}
		select {
		case r := <-done:
			t.Fatalf("the compile ended before tectonic ran: %d %v %s", r.status, r.err, r.body)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("the compile didn't start; stderr %q", c.stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	c.stdin.Close() // the app quits while the compile runs

	r := <-done
	if r.err != nil || r.status != 200 {
		t.Fatalf("compile in flight: %d %v %s", r.status, r.err, r.body)
	}
	var res struct {
		Superseded bool  `json:"superseded"`
		DurationMS int64 `json:"duration_ms"`
	}
	if err := json.Unmarshal(r.body, &res); err != nil || res.Superseded || res.DurationMS < 1500 {
		t.Errorf("compile was cut short: %s (%v)", r.body, err)
	}
	select {
	case <-c.done:
	case <-time.After(30 * time.Second):
		t.Fatal("still running 30s after stdin closed")
	}
	if code := c.cmd.ProcessState.ExitCode(); code != 0 {
		t.Errorf("exit %d; stderr %q", code, c.stderr.String())
	}
}
