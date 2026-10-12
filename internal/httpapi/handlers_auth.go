package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
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
