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

	u, err := h.auth.Register(r.Context(), email, req.Password)
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

	res, err := h.auth.Login(r.Context(), email, req.Password)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, loginResponse{Token: res.Token, ExpiresAt: res.ExpiresAt})
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
// clears it, and it takes no user id — editing somebody else is structurally
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
