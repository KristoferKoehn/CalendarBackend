package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

// verifyEmailResponse carries the confirmed user and a session token.
//
// The token is the point: the link is opened from a mail client, and the next screen asks
// for the details that finish the account. Without a session that screen would have to ask
// for a password first, which would mean typing a password moments after proving the address
// by other means.
type verifyEmailResponse struct {
	User  domain.User `json:"user"`
	Token string      `json:"token"`
}

// verifyEmail redeems a verification link and signs the user in.
//
// Unauthenticated by necessity: the link is opened from a mail client, where there is no
// session. That is safe because the token is the credential — single-use, expiring, and
// stored only as a hash.
func (h *Handler) verifyEmail(w http.ResponseWriter, r *http.Request) {
	var req verifyEmailRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	// Throttled by caller only: there is no account to key on until the token is read.
	if err := h.checkCallerLimit(r, "verify", h.rateLimit.VerifyEmail.ToLimit()); err != nil {
		writeError(w, err)
		return
	}

	if h.emailVerification == nil {
		writeError(w, apierror.InvalidInput(
			"This verification link is invalid or has expired. Request a new one."))
		return
	}

	user, err := h.emailVerification.Verify(r.Context(), req.Token)
	if err != nil {
		h.noteFailure(r, "verify", "")
		writeError(w, err)
		return
	}

	session, err := h.auth.IssueSession(r.Context(), user)
	if err != nil {
		// The address is confirmed and stays confirmed; only the convenience of being
		// signed in was lost. Signing in with the password still works.
		h.logger.Error("could not issue a session after verification",
			"error", err, "userId", user.ID.String())
		writeJSON(w, http.StatusOK, verifyEmailResponse{User: user})
		return
	}

	writeJSON(w, http.StatusOK, verifyEmailResponse{User: user, Token: session.Token})
}

// resendVerification sends a fresh verification link.
//
// Throttled per caller and per account, and it counts every attempt rather than only
// failures: the success case is the one that sends mail, so it is the one worth costing
// something.
func (h *Handler) resendVerification(w http.ResponseWriter, r *http.Request) {
	var req resendVerificationRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	email, err := validation.Email(req.Email)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}

	if err := h.checkAccountLimit(r, "resend", email, h.rateLimit.ResendVerification.ToLimit()); err != nil {
		writeError(w, err)
		return
	}
	if err := h.checkCallerLimit(r, "resend", h.rateLimit.ResendVerification.ToLimit()); err != nil {
		writeError(w, err)
		return
	}
	// Counted as an action, not a failure. Sending a link successfully is the normal case,
	// and treating it as a failure is what let a few ordinary resends exhaust the
	// allowance — including for registration, which shared the same bucket.
	h.consumeAction(r, "resend", email)

	if h.emailVerification == nil {
		// Same answer as an unknown address, so the endpoint cannot be used to probe
		// whether the feature is configured.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sent, err := h.emailVerification.IssueForAddress(r.Context(), email)
	if err != nil {
		// Already verified is the one failure worth reporting: the caller knows their own
		// address, so this tells them nothing they did not already know, and it stops
		// them waiting for a link that will not be sent.
		if apierror.IsCode(err, apierror.CodeConflict) {
			writeError(w, err)
			return
		}
		h.logger.Error("resend-verification failed", "error", err)
	}
	if sent {
		h.logger.Info("verification link requested", "email", email)
	}

	w.WriteHeader(http.StatusNoContent)
}
