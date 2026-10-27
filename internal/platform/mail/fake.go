package mail

import (
	"context"
	"sync"
)

// Fake records sent messages for tests.
type Fake struct {
	mu   sync.Mutex
	Sent []Message
}

func (f *Fake) Send(_ context.Context, message Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Sent = append(f.Sent, message)
	return nil
}

func (f *Fake) Enabled() bool { return true }

// Mu exposes the lock guarding Sent for tests that read it while background
// sends (the welcome email) may still be running.
func (f *Fake) Mu() *sync.Mutex { return &f.mu }
