package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

func (h *Handler) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.users.List(r.Context(), actorFrom(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, usersResponse{Users: users})
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

	u, err := h.users.Update(r.Context(), actorFrom(r), id, core.UpdateUserInput{Role: role, IsAgent: req.IsAgent})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}
