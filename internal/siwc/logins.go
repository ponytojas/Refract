package siwc

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"sync"
	"syscall"
	"time"
)

// ErrLoginBusy means another account on this server is signing in: the
// callback port is fixed, so only one sign-in can wait at a time.
var ErrLoginBusy = errors.New("someone else on this server is signing in to ChatGPT; try again in a few minutes")

// Login states and failure kinds reported by Logins.Status.
const (
	LoginIdle    = "idle"
	LoginPending = "pending"
	LoginDone    = "done"
	LoginFailed  = "failed"

	FailCancelled    = "cancelled"     // cancelled in Refract or denied in ChatGPT
	FailTimeout      = "timeout"       // nobody finished the sign-in in time
	FailPortBusy     = "port_busy"     // another program holds the callback port
	FailOtherAccount = "other_account" // a different account than the saved registration
	FailOther        = "other"
)

// LoginState is one account's in-server sign-in, as the browser sees it. It
// never carries tokens.
type LoginState struct {
	State   string `json:"state"`
	Kind    string `json:"kind,omitempty"`    // when failed
	Message string `json:"message,omitempty"` // when failed with FailOther
}

// Logins runs the ChatGPT sign-in inside the server, for a server on the
// same computer as the browser: the OpenAI redirect to 127.0.0.1 reaches it.
// The credential goes straight to the account's Session; no file is written.
type Logins struct {
	Timeout      time.Duration // 0 = 10 minutes
	Port         int           // as LoginOptions.Port
	AuthorizeURL string        // "" = real endpoint

	mu     sync.Mutex
	cur    *runningLogin
	states map[string]LoginState // by account
}

type runningLogin struct {
	owner  string
	cancel context.CancelFunc
	done   chan struct{}
}

// Start begins a sign-in for s's account and returns the authorize URL for
// the browser to open. A sign-in already running for the same account is
// replaced (a stale tab); one for another account answers ErrLoginBusy.
// fresh registers a new ChatGPT account instead of reusing the saved one.
func (l *Logins) Start(ctx context.Context, s *Session, fresh bool) (string, error) {
	l.mu.Lock()
	if cur := l.cur; cur != nil {
		if cur.owner != s.Owner {
			l.mu.Unlock()
			return "", ErrLoginBusy
		}
		cur.cancel()
		l.mu.Unlock()
		<-cur.done // frees the port
		l.mu.Lock()
		if l.cur != nil { // another start won the race
			l.mu.Unlock()
			return "", ErrLoginBusy
		}
	}
	hostID, err := s.HostID(ctx)
	if err != nil {
		l.mu.Unlock()
		return "", err
	}
	var saved *Credentials
	if !fresh {
		if saved, err = s.Credentials(ctx); err != nil {
			l.mu.Unlock()
			return "", err
		}
		if saved != nil && saved.ClientID == "" {
			saved = nil
		}
	}
	run, cancel := context.WithTimeout(context.Background(), cmp.Or(l.Timeout, 10*time.Minute))
	cur := &runningLogin{owner: s.Owner, cancel: cancel, done: make(chan struct{})}
	l.cur = cur
	l.setState(s.Owner, LoginState{State: LoginPending})
	l.mu.Unlock()

	urls := make(chan string, 1)
	failed := make(chan error, 1)
	go func() {
		gotURL := false
		defer close(cur.done)
		defer cancel()
		c, err := Login(run, LoginOptions{
			HostID: hostID, Saved: saved, Port: l.Port, OAuth: s.OAuth, AuthorizeURL: l.AuthorizeURL,
			OnURL:          func(u string, _ error) { gotURL = true; urls <- u },
			OnRegistration: func(clientID string) error { return s.SaveRegistration(run, clientID) },
		})
		if err == nil {
			err = s.Store(run, c)
		}
		st := LoginState{State: LoginDone}
		if err != nil {
			st = failure(run, err)
			slog.Info("chatgpt sign-in did not complete", "kind", st.Kind)
			if !gotURL {
				failed <- err
			}
		} else {
			slog.Info("chatgpt sign-in completed", "plan_granted", c.PlanGranted())
		}
		l.mu.Lock()
		if l.cur == cur {
			l.cur = nil
		}
		l.setState(s.Owner, st)
		l.mu.Unlock()
	}()
	select {
	case u := <-urls:
		return u, nil
	case err := <-failed: // before the URL: the port is taken
		return "", err
	}
}

// Status reports the account's latest sign-in.
func (l *Logins) Status(owner string) LoginState {
	l.mu.Lock()
	defer l.mu.Unlock()
	if st, ok := l.states[owner]; ok {
		return st
	}
	return LoginState{State: LoginIdle}
}

// Cancel stops the account's running sign-in, if any, and frees the port.
func (l *Logins) Cancel(owner string) {
	l.mu.Lock()
	cur := l.cur
	l.mu.Unlock()
	if cur != nil && cur.owner == owner {
		cur.cancel()
		<-cur.done
	}
}

func (l *Logins) setState(owner string, st LoginState) {
	if l.states == nil {
		l.states = map[string]LoginState{}
	}
	l.states[owner] = st
}

func failure(ctx context.Context, err error) LoginState {
	st := LoginState{State: LoginFailed, Kind: FailOther}
	switch {
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == context.DeadlineExceeded:
		st.Kind = FailTimeout
	case errors.Is(err, context.Canceled), errors.Is(err, ErrDenied):
		st.Kind = FailCancelled
	case errors.Is(err, syscall.EADDRINUSE):
		st.Kind = FailPortBusy
	case errors.Is(err, ErrOtherAccount):
		st.Kind = FailOtherAccount
	default:
		st.Message = err.Error()
	}
	return st
}
