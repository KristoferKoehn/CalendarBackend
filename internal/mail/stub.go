package mail

import (
	"context"
	"sync"
)

// Stub records messages instead of sending them. It is the default implementation,
// so nothing can reach a real address unless a provider is configured explicitly.
//
// Safe for concurrent use. In tests, Sent() is the assertion surface.
type Stub struct {
	mu       sync.Mutex
	sent     []Message
	failWith error
}

// NewStub returns a recording sender.
func NewStub() *Stub { return &Stub{} }

func (s *Stub) Name() string { return "stub" }

func (s *Stub) Send(_ context.Context, m Message) error {
	if err := m.Validate(); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWith != nil {
		return s.failWith
	}
	s.sent = append(s.sent, m)
	return nil
}

// Sent returns a copy of everything recorded so far.
func (s *Stub) Sent() []Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Message, len(s.sent))
	copy(out, s.sent)
	return out
}

// Last returns the most recent message, and whether there was one.
func (s *Stub) Last() (Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.sent) == 0 {
		return Message{}, false
	}
	return s.sent[len(s.sent)-1], true
}

// FailWith makes every subsequent send fail with err. Lets tests exercise the
// caller's error handling without a network.
func (s *Stub) FailWith(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failWith = err
}

// Reset forgets everything recorded and clears any injected failure.
func (s *Stub) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = nil
	s.failWith = nil
}
