package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/store"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	email, err := validation.Email(req.Email)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	if err := validation.Password(req.Password); err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}

	// Registering creates an account, so *every* attempt costs from the address's
	// allowance, successful or not — otherwise an open endpoint could be used to
	// create accounts without limit.
	if err := h.checkCallerLimit(r, "register", h.rateLimit.Register.ToLimit()); err != nil {
		writeError(w, err)
		return
	}

	u, err := h.auth.Register(r.Context(), email, req.Password)
	h.noteFailure(r, "register", email)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, registerResponse{UserID: u.ID.String()})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	email, err := validation.Email(req.Email)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	if err := validation.Password(req.Password); err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}

	// The account limit is checked before the password is compared, so a targeted
	// attack is stopped cheaply. The caller limit is applied below, only once the
	// attempt has failed — see the note in ratelimit.go for why.
	if err := h.checkAccountLimit(r, "login", email, h.rateLimit.Login.ToLimit()); err != nil {
		writeError(w, err)
		return
	}

	res, err := h.auth.Login(r.Context(), email, req.Password)
	if err != nil {
		// Only wrong credentials reach here; a malformed request was rejected
		// above and must not consume anyone's allowance.
		if limitErr := h.checkCallerLimit(r, "login", h.rateLimit.Login.ToLimit()); limitErr != nil {
			writeError(w, limitErr)
			return
		}
		h.noteFailure(r, "login", email)
		writeError(w, err)
		return
	}

	// A correct password clears the account's accrued failures, so a person who
	// mistyped a few times is not left throttled.
	h.clearFailures("login", email)
	writeJSON(w, http.StatusOK, loginResponse{Token: res.Token, ExpiresAt: res.ExpiresAt})
}

// forgotPassword starts a password reset.
//
// It answers 204 with no body in every case where the request was well formed,
// whether or not the address has an account. A different response for an unknown
// address would turn this into a way to enumerate who has signed up, and it would
// do so for anyone on the internet, since the route is necessarily unauthenticated.
func (h *Handler) forgotPassword(w http.ResponseWriter, r *http.Request) {
	var req forgotPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	email, err := validation.Email(req.Email)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}

	// Throttled per address and per account before any work happens, using the same
	// limiter as sign-in so the two cannot be combined to get a larger allowance.
	// Failures are recorded on every attempt: this endpoint sends mail on success,
	// so a success is exactly what an abuser wants and must cost something.
	if err := h.checkAccountLimit(r, "forgot", email, h.rateLimit.ForgotPassword.ToLimit()); err != nil {
		writeError(w, err)
		return
	}
	if err := h.checkCallerLimit(r, "forgot", h.rateLimit.ForgotPassword.ToLimit()); err != nil {
		writeError(w, err)
		return
	}
	h.noteFailure(r, "forgot", email)

	if h.passwordReset == nil {
		// No reset service configured. Answer exactly as for an unknown address, so
		// the endpoint cannot be used to probe whether the feature is enabled.
		w.WriteHeader(http.StatusNoContent)
		return
	}

	sent, err := h.passwordReset.Request(r.Context(), email)
	if err != nil {
		// A storage failure says nothing about the account, so it is safe to report,
		// but it should not be a 500 that reveals the address was found. Record it
		// and answer normally.
		h.logger.Error("forgot-password request failed", "error", err)
	}
	if sent {
		h.logger.Info("password reset requested", "email", email)
	}

	w.WriteHeader(http.StatusNoContent)
}

// resetPassword redeems a reset token and sets a new password.
func (h *Handler) resetPassword(w http.ResponseWriter, r *http.Request) {
	var req resetPasswordRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := validation.Password(req.NewPassword); err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}

	// Throttled by caller only: the token is the credential, and there is no account
	// to key on without parsing it. Limits guessing of the token itself.
	if err := h.checkCallerLimit(r, "reset", h.rateLimit.ResetPassword.ToLimit()); err != nil {
		writeError(w, err)
		return
	}

	if h.passwordReset == nil {
		writeError(w, apierror.InvalidInput("This reset link is invalid or has expired. Request a new one."))
		return
	}

	if err := h.passwordReset.Apply(r.Context(), req.Token, req.NewPassword); err != nil {
		h.noteFailure(r, "reset", "")
		writeError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		writeError(w, apierror.Unauthenticated("Missing token"))
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// updateProfile applies the caller's own profile edits. It reads the body as a
// presence map so that an omitted field is left alone while an explicit null
// clears it, and it takes no user id Ã¢â‚¬â€ editing somebody else is structurally
// impossible.
func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		writeError(w, apierror.Unauthenticated("Missing token"))
		return
	}

	raw, err := decodeRawFields(r)
	if err != nil {
		writeError(w, err)
		return
	}

	displayName, err := profileTextField(raw, "displayName", 100, "displayName")
	if err != nil {
		writeError(w, err)
		return
	}
	pronouns, err := profileTextField(raw, "pronouns", 50, "pronouns")
	if err != nil {
		writeError(w, err)
		return
	}
	jobTitle, err := profileTextField(raw, "jobTitle", 100, "jobTitle")
	if err != nil {
		writeError(w, err)
		return
	}
	bio, err := profileTextField(raw, "bio", 500, "bio")
	if err != nil {
		writeError(w, err)
		return
	}
	birthMonth, birthDay, err := profileBirthdayField(raw)
	if err != nil {
		writeError(w, err)
		return
	}

	updated, err := h.users.UpdateProfile(r.Context(), u, store.ProfileUpdate{
		DisplayName: displayName,
		BirthMonth:  birthMonth,
		BirthDay:    birthDay,
		Pronouns:    pronouns,
		JobTitle:    jobTitle,
		Bio:         bio,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// deleteSelf lets the caller remove their own account. DELETE /users/{id} is
// admin-only, so without this a non-admin would have no way to delete themselves.
// The user's appointments are left in place.
func (h *Handler) deleteSelf(w http.ResponseWriter, r *http.Request) {
	u, ok := userFrom(r.Context())
	if !ok {
		writeError(w, apierror.Unauthenticated("Missing token"))
		return
	}
	deleted, err := h.users.DeleteSelf(r.Context(), u)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, deleted)
}
