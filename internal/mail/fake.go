package mail

import (
	"context"
	"sync"
)

// Fake records messages, for tests. Block, when set, makes Send wait until it
// is closed. Err, when set, is what Send returns (the message is not recorded).
type Fake struct {
	Block chan struct{}
	Err   error

	mu   sync.Mutex
	sent []Message
}

func (f *Fake) Send(ctx context.Context, m Message) error {
	if f.Block != nil {
		select {
		case <-f.Block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if f.Err != nil {
		return f.Err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, m)
	return nil
}

// Sent returns the messages delivered so far.
func (f *Fake) Sent() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Message(nil), f.sent...)
}
