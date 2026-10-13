package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

// listDirectory returns every user to any authenticated caller. This is the
// shared people directory, and it is what makes a display name usable elsewhere.
func (h *Handler) listDirectory(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.Directory(r.Context(), actorFrom(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usersResponse{Users: users})
}

// getUser returns one user's profile. Profiles are visible to every authenticated
// user.
func (h *Handler) getUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	u, err := h.users.Get(r.Context(), actorFrom(r), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) deleteUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	u, err := h.users.Delete(r.Context(), actorFrom(r), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (h *Handler) updateUser(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}

	var req updateUserRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}

	var role *domain.Role
	if req.Role != nil {
		if err := validation.Role(*req.Role); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
		r := domain.Role(*req.Role)
		role = &r
	}

	u, err := h.users.Update(r.Context(), actorFrom(r), id, core.UpdateUserInput{
		Role:          role,
		IsAgent:       req.IsAgent,
		EmailVerified: req.EmailVerified,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}
