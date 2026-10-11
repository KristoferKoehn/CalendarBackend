package mcp

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/validation"
)

func (h *Handler) runAuthRegister(ctx context.Context, _ domain.User, args json.RawMessage) (any, error) {
	var a struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	email, err := validation.Email(a.Email)
	if err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	if err := validation.Password(a.Password); err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	u, err := h.auth.Register(ctx, email, a.Password)
	if err != nil {
		return nil, err
	}
	return map[string]any{"userId": u.ID.String()}, nil
}

func (h *Handler) runAuthLogin(ctx context.Context, _ domain.User, args json.RawMessage) (any, error) {
	var a struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	email, err := validation.Email(a.Email)
	if err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	if err := validation.Password(a.Password); err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	res, err := h.auth.Login(ctx, email, a.Password)
	if err != nil {
		return nil, err
	}
	return map[string]any{"token": res.Token, "expiresAt": res.ExpiresAt}, nil
}

func (h *Handler) runResourcesList(ctx context.Context, actor domain.User, _ json.RawMessage) (any, error) {
	resources, err := h.resources.List(ctx, actor)
	if err != nil {
		return nil, err
	}
	return map[string]any{"resources": resources}, nil
}

func (h *Handler) runAppointmentsCreate(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		ResourceID string `json:"resourceId"`
		Date       string `json:"date"`
		StartTime  string `json:"startTime"`
		EndTime    string `json:"endTime"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	resourceID, err := uuid.Parse(a.ResourceID)
	if err != nil {
		return nil, apierror.InvalidInput("resourceId must be a valid UUID")
	}
	if err := validateTimes(a.Date, a.StartTime, a.EndTime); err != nil {
		return nil, err
	}

	appt, err := h.appointments.Create(ctx, actor, core.CreateAppointmentInput{
		ResourceID: resourceID, Date: a.Date, StartTime: a.StartTime, EndTime: a.EndTime,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"appointmentId": appt.ID.String(),
		"status":        appt.Status,
		"startAt":       appt.StartAt,
		"endAt":         appt.EndAt,
	}, nil
}

func (h *Handler) runAppointmentsCancel(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		AppointmentID string `json:"appointmentId"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	id, err := uuid.Parse(a.AppointmentID)
	if err != nil {
		return nil, apierror.InvalidInput("appointmentId must be a valid UUID")
	}
	return h.appointments.Cancel(ctx, actor, id)
}

func (h *Handler) runAppointmentsReschedule(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		AppointmentID string `json:"appointmentId"`
		Date          string `json:"date"`
		StartTime     string `json:"startTime"`
		EndTime       string `json:"endTime"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	id, err := uuid.Parse(a.AppointmentID)
	if err != nil {
		return nil, apierror.InvalidInput("appointmentId must be a valid UUID")
	}
	if err := validateTimes(a.Date, a.StartTime, a.EndTime); err != nil {
		return nil, err
	}
	return h.appointments.Reschedule(ctx, actor, id, core.RescheduleAppointmentInput{
		Date: a.Date, StartTime: a.StartTime, EndTime: a.EndTime,
	})
}

func (h *Handler) runAppointmentsList(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		ResourceID string `json:"resourceId"`
		From       string `json:"from"`
		To         string `json:"to"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}

	in := core.AppointmentListInput{}
	if a.ResourceID != "" {
		id, err := uuid.Parse(a.ResourceID)
		if err != nil {
			return nil, apierror.InvalidInput("resourceId must be a valid UUID")
		}
		in.ResourceID = &id
	}
	if a.From != "" {
		if err := validation.Date(a.From); err != nil {
			return nil, apierror.InvalidInput(err.Error())
		}
		in.From = &a.From
	}
	if a.To != "" {
		if err := validation.Date(a.To); err != nil {
			return nil, apierror.InvalidInput(err.Error())
		}
		in.To = &a.To
	}

	list, err := h.appointments.List(ctx, actor, in)
	if err != nil {
		return nil, err
	}
	return map[string]any{"appointments": list}, nil
}

func validateTimes(date, start, end string) error {
	if err := validation.Date(date); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	if err := validation.Time(start); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	if err := validation.Time(end); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	if err := validation.TimeRange(start, end); err != nil {
		return apierror.InvalidInput(err.Error())
	}
	return nil
}
