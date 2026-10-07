// Package mail sends transactional application email.
package mail

import "context"

// Message is a single-recipient transactional email.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Sender delivers transactional email. Disabled senders return false from
// Enabled so callers can offer copyable links instead.
type Sender interface {
	Send(context.Context, Message) error
	Enabled() bool
}
