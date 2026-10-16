package mcp

import (
	"context"
	"encoding/json"
	"strings"

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
	if err := h.checkCallerLimit(ctx, "register", h.rateLimit.Register.ToLimit()); err != nil {
		return nil, err
	}
	u, err := h.auth.Register(ctx, email, a.Password)
	// An action, not a failure: the limit bounds how often accounts may be created from one
	// address, so a successful sign-up counts.
	h.consumeAction(ctx, "register", email)
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
	if err := h.checkAccountLimit(ctx, "login", email, h.rateLimit.Login.ToLimit()); err != nil {
		return nil, err
	}
	res, err := h.auth.Login(ctx, email, a.Password)
	if err != nil {
		if limitErr := h.checkCallerLimit(ctx, "login", h.rateLimit.Login.ToLimit()); limitErr != nil {
			return nil, limitErr
		}
		h.noteFailure(ctx, "login", email)
		return nil, err
	}
	h.clearFailures("login", email)
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
		ResourceID  string `json:"resourceId"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Date        string `json:"date"`
		StartTime   string `json:"startTime"`
		EndTime     string `json:"endTime"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	resourceID, err := uuid.Parse(a.ResourceID)
	if err != nil {
		return nil, apierror.InvalidInput("resourceId must be a valid UUID")
	}
	title, err := validation.Title(a.Title)
	if err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	description, err := validation.Description(a.Description)
	if err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	if err := validateTimes(a.Date, a.StartTime, a.EndTime); err != nil {
		return nil, err
	}

	appt, err := h.appointments.Create(ctx, actor, core.CreateAppointmentInput{
		ResourceID: resourceID, Title: title, Description: description, Date: a.Date, StartTime: a.StartTime, EndTime: a.EndTime,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"appointmentId": appt.ID.String(),
		"title":         appt.Title,
		"description":   appt.Description,
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

// runAppointmentsDelete permanently removes an appointment. Authorization is
// enforced by the service (admins only), so a non-admin token gets FORBIDDEN.
func (h *Handler) runAppointmentsDelete(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
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
	if err := h.appointments.Delete(ctx, actor, id); err != nil {
		return nil, err
	}
	return map[string]any{"status": "deleted", "appointmentId": id.String()}, nil
}

// runUsersDelete permanently removes a user account.
//
// Authorization is enforced by the service, not here: admins only, an admin cannot delete their own
// account, and the last remaining admin cannot be deleted. Passing the actor through means the MCP
// and REST transports apply the same rules rather than two copies of them.
func (h *Handler) runUsersDelete(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, apierror.InvalidInput("invalid arguments")
	}
	id, err := uuid.Parse(a.UserID)
	if err != nil {
		return nil, apierror.InvalidInput("userId must be a valid UUID")
	}
	deleted, err := h.users.Delete(ctx, actor, id)
	if err != nil {
		return nil, err
	}
	// The account is gone, so its appointments would otherwise be the only thing naming it. They are
	// kept deliberately; reporting that here means a caller is not left wondering.
	return map[string]any{
		"status": "deleted",
		"userId": deleted.ID.String(),
		"email":  deleted.Email,
		"note":   "the account's appointments have been kept",
	}, nil
}

func (h *Handler) runAppointmentsReschedule(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		AppointmentID string `json:"appointmentId"`
		Title         string `json:"title"`
		Description   string `json:"description"`
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
	title, err := validation.Title(a.Title)
	if err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	description, err := validation.Description(a.Description)
	if err != nil {
		return nil, apierror.InvalidInput(err.Error())
	}
	if err := validateTimes(a.Date, a.StartTime, a.EndTime); err != nil {
		return nil, err
	}
	return h.appointments.Reschedule(ctx, actor, id, core.RescheduleAppointmentInput{
		Title: title, Description: description, Date: a.Date, StartTime: a.StartTime, EndTime: a.EndTime,
	})
}

func (h *Handler) runAppointmentsList(ctx context.Context, actor domain.User, args json.RawMessage) (any, error) {
	var a struct {
		ResourceID string `json:"resourceId"`
		UserID     string `json:"userId"`
		Title      string `json:"title"`
		Status     string `json:"status"`
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
	if a.UserID != "" {
		id, err := uuid.Parse(a.UserID)
		if err != nil {
			return nil, apierror.InvalidInput("userId must be a valid UUID")
		}
		in.UserID = &id
	}
	if a.Title != "" {
		title := strings.TrimSpace(a.Title)
		if len(title) > 200 {
			return nil, apierror.InvalidInput("title query must be at most 200 characters")
		}
		in.Title = &title
	}
	if a.Status != "" {
		if err := validation.AppointmentStatus(a.Status); err != nil {
			return nil, apierror.InvalidInput(err.Error())
		}
		s := domain.AppointmentStatus(a.Status)
		in.Status = &s
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
