// Package mail sends transactional email.
//
// Sending is deliberately a two-implementation affair, following the same pattern
// as internal/store: a real provider behind an interface, and a stub that records
// what it would have sent. That keeps unit tests and the default local setup from
// reaching the network at all — a test run must never be able to email a real
// person.
package mail

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Message is one transactional email.
//
// Text is expected to be filled in. HTML is optional; when both are present the
// provider sends a multipart message and the reader's client decides.
type Message struct {
	To      string
	Subject string
	Text    string
	HTML    string
	// ReplyTo overrides the configured default for this message.
	ReplyTo string
	// From overrides the configured default sender for this message.
	//
	// Sending different kinds of mail from different domains is deliberate: a complaint
	// about one kind damages only that domain's reputation, and each domain can be
	// configured with its own records. The address must be on a domain the provider has
	// verified, or the send is rejected.
	From string
}

// Validate catches the mistakes that would otherwise be rejected by the provider,
// so a configuration error surfaces before a send is attempted.
func (m Message) Validate() error {
	if strings.TrimSpace(m.To) == "" {
		return errors.New("mail: To is required")
	}
	if !strings.Contains(m.To, "@") {
		return fmt.Errorf("mail: To does not look like an address: %q", m.To)
	}
	if strings.TrimSpace(m.Subject) == "" {
		return errors.New("mail: Subject is required")
	}
	if strings.TrimSpace(m.Text) == "" && strings.TrimSpace(m.HTML) == "" {
		return errors.New("mail: a Text or HTML body is required")
	}
	return nil
}

// Sender delivers a Message.
type Sender interface {
	Send(ctx context.Context, m Message) error
	// Name identifies the implementation for logs and diagnostics.
	Name() string
}

// SendError is a failure reported by a provider, with the detail needed to decide
// whether trying again is worth it. A 429 or 5xx is worth retrying; a 4xx is not.
type SendError struct {
	StatusCode int
	Detail     string
}

func (e *SendError) Error() string {
	if e.StatusCode == 0 {
		return fmt.Sprintf("mail: %s", e.Detail)
	}
	return fmt.Sprintf("mail: provider returned %d: %s", e.StatusCode, e.Detail)
}

// Retryable reports whether the same request could succeed later.
//
// A zero status means the request never got a response at all — a connection
// failure or a timeout. The message was probably never accepted, so trying again
// is reasonable. 429 and 5xx are the provider saying so explicitly. Any other 4xx
// is a problem with the request or the account, and repeating it will not help.
func (e *SendError) Retryable() bool {
	if e.StatusCode == 0 {
		return true
	}
	return e.StatusCode == 429 || e.StatusCode >= 500
}
