// Package ratelimit provides a small, dependency-free limiter for the endpoints
// that can be abused: signing in, and creating mail-triggering or account-creating
// requests.
//
// It uses a sliding window over recorded failures rather than a token bucket, so
// that only *failed* attempts count towards a limit. A successful sign-in therefore
// does not consume anyone's allowance, and one mistyped password followed by a
// correct one leaves no lasting penalty.
//
// State is per-process. That is sufficient for a single service instance, which is
// how this is deployed; running more than one instance would need a shared store
// such as the existing PostgreSQL database.
package ratelimit

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ClientIP identifies the caller for rate limiting.
//
// RemoteAddr is authoritative unless proxy headers are explicitly trusted.
// X-Forwarded-For is trivially forged by the client, so trusting it unconditionally
// would let anyone bypass every limit simply by varying the header.
func ClientIP(r *http.Request, trustProxyHeaders bool) string {
	if trustProxyHeaders {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			// The left-most entry is the original client.
			if first := strings.TrimSpace(strings.Split(fwd, ",")[0]); first != "" {
				return first
			}
		}
		if real := strings.TrimSpace(r.Header.Get("X-Real-IP")); real != "" {
			return real
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Limit describes how many failures are tolerated for one key within a window.
type Limit struct {
	Failures int
	Window   time.Duration
}

// entry records recent failures for one key. Timestamps are kept oldest-first.
type entry struct {
	failures []time.Time
}

// Limiter tracks failures per key and reports when a key should be refused.
type Limiter struct {
	mu      sync.Mutex
	entries map[string]*entry
	now     func() time.Time

	// pruneEvery controls how often stale keys are collected. Derived from the
	// widest window so nothing that could still be relevant is dropped early.
	pruneEvery time.Duration
	stop       chan struct{}
	stopOnce   sync.Once
}

// New returns a started Limiter. Call Close to stop its background sweeper.
//
// pruneEvery should be at least as long as the widest window in use, since a
// failure is only forgettable once it has fallen outside that window.
func New(pruneEvery time.Duration) *Limiter {
	return newWithClock(pruneEvery, time.Now)
}

// newWithClock exists so tests can drive time forward without sleeping. Unexported
// deliberately: production always uses the wall clock.
func newWithClock(pruneEvery time.Duration, now func() time.Time) *Limiter {
	if pruneEvery <= 0 {
		pruneEvery = 15 * time.Minute
	}
	l := &Limiter{
		entries:    make(map[string]*entry),
		now:        now,
		pruneEvery: pruneEvery,
		stop:       make(chan struct{}),
	}
	go l.sweep()
	return l
}

// Close stops the background sweeper. Safe to call more than once.
func (l *Limiter) Close() {
	l.stopOnce.Do(func() { close(l.stop) })
}

// Allow reports whether the key may make another attempt, and how long to wait if
// it may not. The wait is only meaningful when allowed is false.
func (l *Limiter) Allow(key string, limit Limit) (allowed bool, retryAfter time.Duration) {
	if limit.Failures <= 0 || limit.Window <= 0 {
		return true, 0
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	cutoff := now.Add(-limit.Window)

	e := l.entries[key]
	if e == nil {
		return true, 0
	}

	// Drop failures that have aged out, and count what remains.
	kept := e.failures[:0]
	for _, t := range e.failures {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	e.failures = kept

	if len(e.failures) < limit.Failures {
		return true, 0
	}

	// Blocked until the oldest failure in the window ages out, which is the
	// earliest moment an attempt could succeed.
	oldest := e.failures[0]
	wait := oldest.Add(limit.Window).Sub(now)
	if wait < 0 {
		wait = 0
	}
	return false, wait
}

// Fail records a failed attempt for the key.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	e := l.entries[key]
	if e == nil {
		e = &entry{}
		l.entries[key] = e
	}
	e.failures = append(e.failures, now)
}

// Reset forgets a key's failures, used after a successful attempt so a legitimate
// sign-in clears any accrued penalty.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.entries, key)
}

// Sweep removes keys with no recent failures. Exported so it can be driven
// directly in tests instead of waiting for the timer.
func (l *Limiter) Sweep() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := l.now().Add(-l.pruneEvery)
	for key, e := range l.entries {
		kept := e.failures[:0]
		for _, t := range e.failures {
			if t.After(cutoff) {
				kept = append(kept, t)
			}
		}
		if len(kept) == 0 {
			delete(l.entries, key)
			continue
		}
		e.failures = kept
	}
}

// Size reports how many keys are tracked. Used by tests to confirm sweeping.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.entries)
}

func (l *Limiter) sweep() {
	ticker := time.NewTicker(l.pruneEvery)
	defer ticker.Stop()
	for {
		select {
		case <-l.stop:
			return
		case <-ticker.C:
			l.Sweep()
		}
	}
}
