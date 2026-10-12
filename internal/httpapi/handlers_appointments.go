package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

func (h *Handler) createAppointment(w http.ResponseWriter, r *http.Request) {
	var req createAppointmentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	resourceID, err := uuid.Parse(req.ResourceID)
	if err != nil {
		writeError(w, apierror.InvalidInput("resourceId must be a valid UUID"))
		return
	}
	title, err := validation.Title(req.Title)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	description, err := validation.Description(req.Description)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	if err := validateAppointmentTimes(req.Date, req.StartTime, req.EndTime); err != nil {
		writeError(w, err)
		return
	}

	a, err := h.appointments.Create(r.Context(), actorFrom(r), core.CreateAppointmentInput{
		ResourceID:  resourceID,
		Title:       title,
		Description: description,
		Date:        req.Date,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, a)
}

func (h *Handler) listAppointments(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	in := core.AppointmentListInput{}

	if v := q.Get("resourceId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, apierror.InvalidInput("resourceId must be a valid UUID"))
			return
		}
		in.ResourceID = &id
	}
	if v := q.Get("userId"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			writeError(w, apierror.InvalidInput("userId must be a valid UUID"))
			return
		}
		in.UserID = &id
	}
	if v := strings.TrimSpace(q.Get("title")); v != "" {
		if len(v) > 200 {
			writeError(w, apierror.InvalidInput("title query must be at most 200 characters"))
			return
		}
		in.Title = &v
	}
	if v := q.Get("from"); v != "" {
		if err := validation.Date(v); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
		in.From = &v
	}
	if v := q.Get("to"); v != "" {
		if err := validation.Date(v); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
		in.To = &v
	}
	if v := q.Get("status"); v != "" {
		if err := validation.AppointmentStatus(v); err != nil {
			writeError(w, apierror.InvalidInput(err.Error()))
			return
		}
		s := domain.AppointmentStatus(v)
		in.Status = &s
	}

	list, err := h.appointments.List(r.Context(), actorFrom(r), in)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, appointmentsResponse{Appointments: list})
}

func (h *Handler) cancelAppointment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	a, err := h.appointments.Cancel(r.Context(), actorFrom(r), id)
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

func (h *Handler) rescheduleAppointment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}

	var req rescheduleAppointmentRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, err)
		return
	}
	title, err := validation.Title(req.Title)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	description, err := validation.Description(req.Description)
	if err != nil {
		writeError(w, apierror.InvalidInput(err.Error()))
		return
	}
	if err := validateAppointmentTimes(req.Date, req.StartTime, req.EndTime); err != nil {
		writeError(w, err)
		return
	}

	a, err := h.appointments.Reschedule(r.Context(), actorFrom(r), id, core.RescheduleAppointmentInput{
		Title:       title,
		Description: description,
		Date:        req.Date,
		StartTime:   req.StartTime,
		EndTime:     req.EndTime,
	})
	if err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, a)
}

// deleteAppointment erases an appointment outright. Admin-only, because it
// removes the record rather than cancelling it. Used to clear appointments whose
// owner account has been deleted.
func (h *Handler) deleteAppointment(w http.ResponseWriter, r *http.Request) {
	id, ok := pathUUID(w, r)
	if !ok {
		return
	}
	if err := h.appointments.Delete(r.Context(), actorFrom(r), id); err != nil {
		writeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted", "id": id.String()})
}
