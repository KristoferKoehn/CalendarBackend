package mcp

import (
	"context"
	"strings"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
)

// Throttling here mirrors the REST transport deliberately, and the key shapes are
// identical so that an attacker cannot spread attempts across the two transports:
//
//	<scope>:acct:<sha256 of lowercased email>
//	<scope>:ip:<address>
//
// As in the REST transport, the account limit is checked before the attempt and the
// caller limit only after a failure, so a shared address with accrued failures can
// still sign in successfully.
//
// JSON-RPC has no HTTP status to spend on this, so a refusal travels as a JSON-RPC
// error carrying the contract code in `data.code`, which is how every other tool
// error is reported.

func accountKey(scope, email string) string {
	return scope + ":acct:" + auth.HashToken(strings.ToLower(email))
}

func ipKey(scope, ip string) string {
	return scope + ":ip:" + ip
}

func (h *Handler) callerIP(ctx context.Context) string {
	r, ok := requestFrom(ctx)
	if !ok {
		// No request in context (only possible in tests); fall back to a single
		// shared bucket rather than silently skipping the limit.
		return "unknown"
	}
	return ratelimit.ClientIP(r, h.trustProxyHeaders)
}

// checkAccountLimit refuses the attempt if this account has too many recent
// failures.
func (h *Handler) checkAccountLimit(ctx context.Context, scope, email string, limit ratelimit.Limit) error {
	if h.limiter == nil || email == "" {
		return nil
	}
	if allowed, retry := h.limiter.Allow(accountKey(scope, email), limit); !allowed {
		return apierror.RateLimited("Too many attempts for this account. Try again later.", retry)
	}
	return nil
}

// checkCallerLimit refuses the attempt if this address has too many recent
// failures. Only call this on a path that has already failed.
func (h *Handler) checkCallerLimit(ctx context.Context, scope string, limit ratelimit.Limit) error {
	if h.limiter == nil {
		return nil
	}
	ip := h.callerIP(ctx)
	if allowed, retry := h.limiter.Allow(ipKey(scope, ip), limit); !allowed {
		return apierror.RateLimited("Too many attempts. Try again later.", retry)
	}
	return nil
}

func (h *Handler) noteFailure(ctx context.Context, scope, email string) {
	if h.limiter == nil {
		return
	}
	h.limiter.Fail(ipKey(scope, h.callerIP(ctx)))
	if email != "" {
		h.limiter.Fail(accountKey(scope, email))
	}
}

func (h *Handler) clearFailures(scope, email string) {
	if h.limiter == nil || email == "" {
		return
	}
	h.limiter.Reset(accountKey(scope, email))
}
