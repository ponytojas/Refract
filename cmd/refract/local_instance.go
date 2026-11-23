package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// localLockFile is the file in the data folder that a running refract local
// holds locked. While it holds the lock it also records its pid and, once it
// listens, where.
const localLockFile = "local.lock"

// instanceWait bounds how long a second start waits for a running instance
// that has recorded its address to answer /healthz.
var instanceWait = 8 * time.Second

// instanceStartingWait bounds how long a second start waits for an instance
// that holds the lock but doesn't listen yet: finding the key can wait for
// the system credential store (up to 10 s a call) and for another start's key
// lock (keyLockWait), and the database may be upgrading.
var instanceStartingWait = 60 * time.Second

// Instance states reported by `refract local --status`.
const (
	stateRunning      = "running"      // holds the lock and answers /healthz
	stateStarting     = "starting"     // holds the lock, recorded its pid, doesn't listen yet
	stateStopped      = "stopped"      // nobody holds the lock
	stateUnresponsive = "unresponsive" // holds the lock, recorded an address that doesn't answer (or nothing usable)
)

// statusExitStarting is the exit code of `refract local --status` for
// stateStarting, so a wrapper can wait without reading the JSON. Running is
// 0; stopped and unresponsive are 1.
const statusExitStarting = 2

// instanceInfo is what the running instance writes into the lock file: its
// pid and version when it takes the lock, and its URL once it listens.
type instanceInfo struct {
	URL     string `json:"url,omitempty"`
	PID     int    `json:"pid"`
	Version string `json:"version"`
	// Embedded: started with --embedded, so every request needs that
	// launch's token and the address can't simply be opened.
	Embedded bool `json:"embedded,omitempty"`
}

// instanceStatus is the machine-readable answer of `refract local --status`,
// printed as one JSON line.
type instanceStatus struct {
	State   string `json:"state"`
	URL     string `json:"url,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Version string `json:"version,omitempty"`
	// Embedded: the instance needs its own launch's token (see instanceInfo).
	Embedded bool   `json:"embedded,omitempty"`
	DataDir  string `json:"dataDir"`
	Lock     string `json:"lockFile"`
}

// instanceLock is the exclusive lock on a data folder's lock file.
type instanceLock struct {
	f        *os.File
	embedded bool // recorded with the address
}

// openLockFile opens dataDir's lock file, creating it when create is set. It
// never follows a symbolic link and accepts only a regular file, so nothing
// written to the lock can land in another file.
func openLockFile(dataDir string, create bool) (*os.File, error) {
	path := filepath.Join(dataDir, localLockFile)
	before, err := os.Lstat(path)
	switch {
	case err == nil && !before.Mode().IsRegular():
		return nil, fmt.Errorf("%s is not a regular file (%v); remove it and start again", path, before.Mode().Type())
	case err != nil && !(create && errors.Is(err, fs.ErrNotExist)):
		return nil, err
	}
	flags := os.O_RDWR | noFollow
	if create {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening the lock file: %w", err)
	}
	fi, err := f.Stat()
	if err == nil && (!fi.Mode().IsRegular() || before != nil && !os.SameFile(before, fi)) {
		err = fmt.Errorf("%s changed while it was opened, or is not a regular file; remove it and start again", path)
	}
	if err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// lockInstance takes the lock of dataDir for this process. held is true when
// another process has it. The lock, not the file's existence, decides: a
// file left by a crash is simply locked again, and its stale address (which
// may now belong to another program) is replaced by this process's pid.
func lockInstance(dataDir string) (l *instanceLock, held bool, err error) {
	f, err := openLockFile(dataDir, true)
	if err != nil {
		return nil, false, err
	}
	if held, err = lockFile(f); held || err != nil {
		f.Close()
		if err != nil {
			err = fmt.Errorf("locking %s: %w", f.Name(), err)
		}
		return nil, held, err
	}
	l = &instanceLock{f: f}
	if err := l.write(instanceInfo{PID: os.Getpid(), Version: version}); err != nil {
		f.Close()
		return nil, false, fmt.Errorf("writing %s: %w", f.Name(), err)
	}
	return l, false, nil
}

// lockHeld reports whether a process holds dataDir's lock, without creating
// or changing the lock file. A missing file means nobody holds it.
func lockHeld(dataDir string) (bool, error) {
	f, err := openLockFile(dataDir, false)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close() // closing drops the lock if this probe took it
	held, err := lockFile(f)
	if err != nil {
		return false, fmt.Errorf("locking %s: %w", f.Name(), err)
	}
	return held, nil
}

// Record adds the address being served to what the lock file says.
func (l *instanceLock) Record(u string) error {
	return l.write(instanceInfo{URL: u, PID: os.Getpid(), Version: version, Embedded: l.embedded})
}

func (l *instanceLock) write(info instanceInfo) error {
	b, _ := json.Marshal(info)
	if err := l.f.Truncate(0); err != nil {
		return err
	}
	_, err := l.f.WriteAt(append(b, '\n'), 0)
	return err
}

// Release gives up the lock and clears what it recorded.
func (l *instanceLock) Release() {
	l.f.Truncate(0)
	l.f.Close() // closing drops the lock
}

func readInstanceInfo(dataDir string) (instanceInfo, error) {
	var info instanceInfo
	f, err := openLockFile(dataDir, false)
	if err != nil {
		return info, err
	}
	defer f.Close()
	err = json.NewDecoder(f).Decode(&info)
	return info, err
}

// loopbackURL accepts only an http URL on 127.0.0.1 or ::1, so a doctored
// lock file can't make refract contact or open anything else.
func loopbackURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "http" || u.Port() == "" || u.Path != "" {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// healthClient checks /healthz on the recorded address only: it doesn't
// follow redirects, which could lead off loopback or to another server.
var healthClient = &http.Client{
	Timeout:       2 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// checkInstance describes the refract local using dataDir, given that its lock
// was held. It waits up to instanceWait for a recorded address to answer
// /healthz with 200, and while the holder is still starting (pid recorded, no
// address yet) up to startingWait as well. It reports stopped as soon as the
// holder lets go of the lock (it was shutting down). It contacts loopback
// addresses only.
func checkInstance(ctx context.Context, dataDir string, startingWait time.Duration) instanceStatus {
	st := instanceStatus{DataDir: dataDir, Lock: filepath.Join(dataDir, localLockFile)}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	since := time.Now()
	listening := false
	for {
		st.State, st.URL = stateUnresponsive, ""
		// An empty or half-written file while the holder starts: retry.
		if info, err := readInstanceInfo(dataDir); err == nil {
			st.PID, st.Version, st.Embedded = info.PID, info.Version, info.Embedded
			switch {
			case info.URL == "" && info.PID > 0:
				st.State = stateStarting
			case loopbackURL(info.URL):
				if !listening { // the wait for an answer starts when it listens
					listening, since = true, time.Now()
				}
				st.URL = info.URL
				req, _ := http.NewRequestWithContext(ctx, http.MethodGet, info.URL+"/healthz", nil)
				if resp, err := healthClient.Do(req); err == nil {
					resp.Body.Close()
					if resp.StatusCode == http.StatusOK {
						st.State = stateRunning
						return st
					}
				}
			}
		}
		if held, err := lockHeld(dataDir); err == nil && !held {
			return instanceStatus{State: stateStopped, DataDir: st.DataDir, Lock: st.Lock}
		}
		limit := instanceWait
		if st.State == stateStarting {
			limit = max(limit, startingWait)
		}
		if time.Since(since) >= limit {
			return st
		}
		select {
		case <-ctx.Done():
			return st
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// recoveryHelp tells what to do when the lock is held but nothing answers.
func recoveryHelp(st instanceStatus) string {
	var b strings.Builder
	fmt.Fprintf(&b, "another refract local holds the lock on the data folder %s", st.DataDir)
	switch {
	case st.State == stateStarting:
		fmt.Fprintf(&b, " but is still starting after %v: it may be waiting for the system credential store (unlock it), or upgrading the database", instanceStartingWait)
	case st.URL != "":
		fmt.Fprintf(&b, " but %s doesn't answer", st.URL)
	default:
		b.WriteString(" but has not recorded an address")
	}
	b.WriteString(".\nWait a moment and try again; if it stays like this, stop that process")
	if st.PID > 0 {
		fmt.Fprintf(&b, " (pid %d)", st.PID)
	} else {
		b.WriteString(" (see its pid in " + st.Lock + ")")
	}
	b.WriteString(" and start again. Nothing was deleted: the lock is released when the process ends, and " + localLockFile + " is safe to leave in place")
	return b.String()
}

// errExit is an error that ends the program with a given exit code. An empty
// message prints nothing.
type errExit struct {
	code int
	msg  string
}

func (e errExit) Error() string { return e.msg }

// localExitCode reports err the way refract local does and returns the exit code.
func localExitCode(err error) int {
	var e errExit
	switch {
	case err == nil:
		return 0
	case errors.As(err, &e):
		if e.msg != "" {
			fmt.Fprintln(os.Stderr, "refract local:", e.msg)
		}
		return e.code
	}
	fmt.Fprintln(os.Stderr, "refract local:", err)
	return 1
}
