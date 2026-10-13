package httpapi

import (
	"net/http"
	"strings"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
)

// Throttle keys share a documented shape so the REST and MCP transports draw on
// the same allowance for the same action and account:
//
//	<scope>:acct:<sha256 of lowercased email>
//	<scope>:ip:<address>
//
// The email is hashed so raw addresses are not held in the limiter's memory.
//
// The two scopes are applied at different moments, deliberately:
//
//   - The account limit is checked *before* the attempt, because a targeted attack
//     must be stopped before a password is even compared.
//   - The caller limit is checked only when an attempt has *failed*. Checking it up
//     front would refuse a correct password once the address had accumulated
//     failures for other accounts, which is exactly the "shared office IP" case.
//     A failed-attempt limit should never lock out someone who succeeds.

func accountKey(scope, email string) string {
	return scope + ":acct:" + auth.HashToken(strings.ToLower(email))
}

func ipKey(scope, ip string) string {
	return scope + ":ip:" + ip
}

// checkAccountLimit refuses the attempt if this account has too many recent
// failures. Safe to call before doing any work.
func (h *Handler) checkAccountLimit(r *http.Request, scope, email string, limit ratelimit.Limit) error {
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
func (h *Handler) checkCallerLimit(r *http.Request, scope string, limit ratelimit.Limit) error {
	if h.limiter == nil {
		return nil
	}
	ip := ratelimit.ClientIP(r, h.trustProxyHeaders)
	if allowed, retry := h.limiter.Allow(ipKey(scope, ip), limit); !allowed {
		return apierror.RateLimited("Too many attempts. Try again later.", retry)
	}
	return nil
}

// noteFailure records a failed attempt against both the caller and the account.
//
// "Failed" is the operative word: this is for a wrong password or a wrong token. Naming it
// for failures keeps callers honest about which they are recording, because the two mean
// opposite things — a failure means something went wrong, an action means it went right.
func (h *Handler) noteFailure(r *http.Request, scope, email string) {
	if h.limiter == nil {
		return
	}
	ip := ratelimit.ClientIP(r, h.trustProxyHeaders)
	h.limiter.Fail(ipKey(scope, ip))
	if email != "" {
		h.limiter.Fail(accountKey(scope, email))
	}
}

// consumeAction records a request that succeeded, because the rate limit on registering or
// sending mail is about how often the action may happen, not about whether it worked.
//
// Kept separate from noteFailure deliberately. Sharing one record call is what allowed a
// few ordinary sign-ups and resends to exhaust an allowance and block the address.
func (h *Handler) consumeAction(r *http.Request, scope, email string) {
	h.noteFailure(r, scope, email)
}

// clearFailures forgets the account's failures after a success, so a person who
// mistyped a few times is not left throttled.
func (h *Handler) clearFailures(scope, email string) {
	if h.limiter == nil || email == "" {
		return
	}
	h.limiter.Reset(accountKey(scope, email))
}
