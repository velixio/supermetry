package notifications

import (
	"context"
	"sync"
)

// Recorded is one event captured by Fake.
type Recorded struct {
	OrgID int64
	Event Event
}

// Fake is a Notifier for tests that records every event it receives.
type Fake struct {
	mu     sync.Mutex
	events []Recorded
}

func (f *Fake) Notify(_ context.Context, orgID int64, event Event) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, Recorded{OrgID: orgID, Event: event})
}

// Events returns a copy of what was recorded so far.
func (f *Fake) Events() []Recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Recorded(nil), f.events...)
}
