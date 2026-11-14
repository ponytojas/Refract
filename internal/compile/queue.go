package compile

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ponytojas/Refract/internal/auth"
)

// ErrBusy means a compile waited MaxWait for its turn and gave up.
var ErrBusy = errors.New("the server is busy compiling; try again in a moment")

type assistantKey struct{}

// AsAssistant marks the compiles made with the returned context as the
// assistant's: they queue behind every account's interactive compile and are
// never replaced by a newer one.
func AsAssistant(ctx context.Context) context.Context {
	return context.WithValue(ctx, assistantKey{}, true)
}

// Queue limits how many compiles run at once, and lets one account run one
// at a time, so a single user (or the assistant, up to 25 compiles a turn)
// can't take every slot.
type Queue struct {
	Next    Compiler
	Workers int           // compiles running at once; at least 1
	MaxWait time.Duration // longest a compile waits for its turn; 0 = no limit

	mu       sync.Mutex
	running  int
	busy     map[string]bool     // accounts with a running compile
	accounts map[string]*account // accounts with compiles waiting
	order    []string            // the accounts of the map, by arrival
	served   uint64              // counts the turns granted
}

type account struct {
	lastTurn    uint64 // when it last got a slot; the lowest goes first
	interactive *waiter
	assistant   []*waiter
}

type waiter struct {
	key      string
	done     chan struct{} // closed once granted or replaced
	granted  bool
	replaced bool
}

func (q *Queue) Compile(ctx context.Context, projectDir, mainFile string) (*Result, error) {
	if ctx.Err() != nil {
		return superseded(), nil
	}
	key := ""
	if u, ok := auth.UserFrom(ctx); ok {
		key = u.ID
	}
	assistant, _ := ctx.Value(assistantKey{}).(bool)
	w := &waiter{key: key, done: make(chan struct{})}

	q.mu.Lock()
	q.enqueue(w, assistant)
	q.dispatch()
	q.mu.Unlock()

	var expired <-chan time.Time
	if q.MaxWait > 0 {
		timer := time.NewTimer(q.MaxWait)
		defer timer.Stop()
		expired = timer.C
	}
	var giveUp error
	select {
	case <-w.done:
	case <-ctx.Done():
	case <-expired:
		giveUp = ErrBusy
	}
	if giveUp != nil || ctx.Err() != nil {
		q.mu.Lock()
		if w.granted { // granted at the moment it gave up: hand the slot back
			q.releaseLocked(w)
		} else {
			q.remove(w)
		}
		q.mu.Unlock()
		if giveUp != nil {
			return nil, giveUp
		}
		return superseded(), nil
	}
	if w.replaced {
		return superseded(), nil
	}
	defer func() {
		q.mu.Lock()
		q.releaseLocked(w)
		q.mu.Unlock()
	}()
	return q.Next.Compile(ctx, projectDir, mainFile)
}

func superseded() *Result { return &Result{Superseded: true, Problems: []Problem{}} }

func (q *Queue) enqueue(w *waiter, assistant bool) {
	if q.accounts == nil {
		q.accounts = map[string]*account{}
		q.busy = map[string]bool{}
	}
	a := q.accounts[w.key]
	if a == nil {
		a = &account{}
		q.accounts[w.key] = a
		q.order = append(q.order, w.key)
	}
	if assistant {
		a.assistant = append(a.assistant, w)
		return
	}
	if old := a.interactive; old != nil {
		old.replaced = true
		close(old.done)
	}
	a.interactive = w
}

// remove takes a waiting compile out of its account.
func (q *Queue) remove(w *waiter) {
	a := q.accounts[w.key]
	if a == nil {
		return
	}
	if a.interactive == w {
		a.interactive = nil
	}
	for i, x := range a.assistant {
		if x == w {
			a.assistant = append(a.assistant[:i], a.assistant[i+1:]...)
			break
		}
	}
	q.forget(w.key)
}

// forget drops an account that has nothing waiting.
func (q *Queue) forget(key string) {
	a := q.accounts[key]
	if a == nil || a.interactive != nil || len(a.assistant) > 0 {
		return
	}
	delete(q.accounts, key)
	for i, k := range q.order {
		if k == key {
			q.order = append(q.order[:i], q.order[i+1:]...)
			break
		}
	}
}

func (q *Queue) releaseLocked(w *waiter) {
	q.running--
	delete(q.busy, w.key)
	q.dispatch()
}

// dispatch grants slots while there are free ones and someone can use them.
// Accounts go round-robin (the one served longest ago first): first those
// with an interactive compile waiting, then those with only the assistant's.
func (q *Queue) dispatch() {
	for q.running < max(q.Workers, 1) {
		var pick *account
		var pickKey string
		for _, interactive := range []bool{true, false} {
			for _, k := range q.order {
				a := q.accounts[k]
				if q.busy[k] || (interactive && a.interactive == nil) || (!interactive && len(a.assistant) == 0) {
					continue
				}
				if pick == nil || a.lastTurn < pick.lastTurn {
					pick, pickKey = a, k
				}
			}
			if pick != nil {
				break
			}
		}
		if pick == nil {
			return
		}
		var w *waiter
		if pick.interactive != nil {
			w, pick.interactive = pick.interactive, nil
		} else {
			w, pick.assistant = pick.assistant[0], pick.assistant[1:]
		}
		q.served++
		pick.lastTurn = q.served
		q.running++
		q.busy[pickKey] = true
		w.granted = true
		close(w.done)
		q.forget(pickKey)
	}
}
