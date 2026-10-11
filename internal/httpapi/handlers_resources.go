package httpapi

import (
	"net/http"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

func (h *Handler) listResources(w http.ResponseWriter, r *http.Request) {
	resources, err := h.resources.List(r.Context(), actorFrom(r))
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, resourcesResponse{Resources: resources})
}

func (h *Handler) createResource(w http.ResponseWriter, r *http.Request) {
	var req createResourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if err := validation.Name(req.Name); err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	if req.Timezone != nil {
		if err := validation.Timezone(*req.Timezone); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
	}

	rsc, err := h.resources.Create(r.Context(), actorFrom(r), core.CreateResourceInput{
		Name:        req.Name,
		Description: req.Description,
		Timezone:    req.Timezone,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rsc)
}

func (h *Handler) updateResource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}

	var req updateResourceRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	if req.Name != nil {
		if err := validation.Name(*req.Name); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
	}
	if req.Timezone != nil {
		if err := validation.Timezone(*req.Timezone); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
	}

	rsc, err := h.resources.Update(r.Context(), actorFrom(r), id, core.UpdateResourceInput{
		Name:        req.Name,
		Description: req.Description,
		Timezone:    req.Timezone,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rsc)
}

func (h *Handler) deactivateResource(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	rsc, err := h.resources.Deactivate(r.Context(), actorFrom(r), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rsc)
}
