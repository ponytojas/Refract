package mail

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

const queueSize = 256

// Outbox sends messages off the request path: one worker, a bounded queue, so
// how long a request takes never depends on the mail server.
type Outbox struct {
	sender Sender
	queue  chan Message
	mu     sync.Mutex
	closed bool
	done   chan struct{}
}

// NewOutbox starts the worker.
func NewOutbox(s Sender) *Outbox {
	o := &Outbox{sender: s, queue: make(chan Message, queueSize), done: make(chan struct{})}
	go o.run()
	return o
}

func (o *Outbox) run() {
	defer close(o.done)
	for m := range o.queue {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := o.sender.Send(ctx, m); err != nil {
			slog.Warn("email not sent", "subject", m.Subject, "err", err)
		}
		cancel()
	}
}

// Send queues a message and never blocks: when the queue is full or the outbox
// is closed, the message is dropped with a warning.
func (o *Outbox) Send(m Message) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		slog.Warn("email dropped: shutting down")
		return
	}
	select {
	case o.queue <- m:
	default:
		slog.Warn("email dropped: the queue is full")
	}
}

// Drain stops accepting messages and waits for the queued ones, up to timeout.
// It reports whether the queue emptied.
func (o *Outbox) Drain(timeout time.Duration) bool {
	o.mu.Lock()
	if !o.closed {
		o.closed = true
		close(o.queue)
	}
	o.mu.Unlock()
	select {
	case <-o.done:
		return true
	case <-time.After(timeout):
		return false
	}
}
