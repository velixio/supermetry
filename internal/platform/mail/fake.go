package mail

import "context"

// Fake records sent messages for tests.
type Fake struct {
	Sent []Message
}

func (f *Fake) Send(_ context.Context, message Message) error {
	f.Sent = append(f.Sent, message)
	return nil
}

func (f *Fake) Enabled() bool { return true }
