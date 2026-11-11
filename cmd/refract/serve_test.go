package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

// startServe listens on a free port and runs serve with h until the returned
// cancel is called; done receives what serve returned.
func startServe(t *testing.T, h http.HandlerFunc, drain, settle time.Duration) (url string, cancel context.CancelFunc, done <-chan error) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := make(chan error, 1)
	go func() { out <- serve(ctx, &http.Server{Handler: h}, ln, drain, settle) }()
	return "http://" + ln.Addr().String(), cancel, out
}

func TestServeWaitsForRequestsInFlight(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	url, cancel, done := startServe(t, func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Write([]byte("ok"))
	}, 5*time.Second, time.Second)

	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(url)
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		got <- result{resp.StatusCode, string(b), err}
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		t.Fatalf("serve returned (%v) with a request in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case r := <-got:
		if r.err != nil || r.status != http.StatusOK || r.body != "ok" {
			t.Errorf("request = %d %q %v", r.status, r.body, r.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request never finished")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve didn't return after the request finished")
	}
}

// An assistant stream outlives the drain: it is cancelled, and serve waits
// for it to record how it ended before the database may close.
func TestServeClosesStreamsThenWaits(t *testing.T) {
	started := make(chan struct{})
	var recorded atomic.Bool
	url, cancel, done := startServe(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
		time.Sleep(50 * time.Millisecond)
		recorded.Store(true)
	}, 100*time.Millisecond, 2*time.Second)

	go func() {
		if resp, err := http.Get(url); err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve = %v", err)
		}
		if !recorded.Load() {
			t.Error("serve returned before the cancelled handler finished")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve didn't return")
	}
}

func TestServeReturnsServeError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	done := make(chan error, 1)
	go func() {
		done <- serve(context.Background(), &http.Server{Handler: http.NotFoundHandler()}, ln, time.Second, time.Second)
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("serve on a closed listener returned nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve didn't return")
	}
}

// A listener can fail after requests started: they still get to finish
// before serve returns its error, and with it the database closes.
func TestServeErrorStillWaits(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- serve(context.Background(), &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
			finished.Store(true)
		})}, ln, 5*time.Second, time.Second)
	}()
	go func() {
		if resp, err := http.Get("http://" + ln.Addr().String()); err == nil {
			resp.Body.Close()
		}
	}()
	<-started
	ln.Close()
	select {
	case err := <-done:
		t.Fatalf("serve returned (%v) with a request in flight", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-done:
		if err == nil {
			t.Error("serve hid the listener's error")
		}
		if !finished.Load() {
			t.Error("serve returned before the request finished")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("serve didn't return")
	}
}
