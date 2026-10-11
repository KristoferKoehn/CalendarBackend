// Package core holds the use-case services and their shared input types.
package core

import (
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

// CreateResourceInput is the create-resource request body.
type CreateResourceInput struct {
	Name        string
	Description *string
	Timezone    *string
}

// UpdateResourceInput is the partial update-resource request body.
type UpdateResourceInput struct {
	Name        *string
	Description *string
	Timezone    *string
}

// UpdateUserInput is the update-user request body.
type UpdateUserInput struct {
	Role    *domain.Role
	IsAgent *bool
}

// CreateAppointmentInput is the create-appointment request body.
type CreateAppointmentInput struct {
	ResourceID uuid.UUID
	Date       string
	StartTime  string
	EndTime    string
}

// RescheduleAppointmentInput is the reschedule request body.
type RescheduleAppointmentInput struct {
	Date      string
	StartTime string
	EndTime   string
}

// AppointmentListInput carries optional list filters. From and To are calendar
// dates in America/Los_Angeles.
type AppointmentListInput struct {
	ResourceID *uuid.UUID
	UserID     *uuid.UUID
	From       *string
	To         *string
	Status     *domain.AppointmentStatus
}

// LoginResult is the successful login response.
type LoginResult struct {
	Token     string
	ExpiresAt time.Time
	User      domain.User
}

// requireAdmin rejects non-admin callers.
func requireAdmin(actor domain.User) error {
	if actor.Role != domain.RoleAdmin {
		return apierror.Forbidden("Admin access required")
	}
	return nil
}

// canModifyAppointment reports whether the actor may cancel or reschedule it.
func canModifyAppointment(actor domain.User, a domain.Appointment) bool {
	return actor.Role == domain.RoleAdmin || a.UserID == actor.ID
}

// orNow returns now, or a UTC time.Now when now is nil.
func orNow(now func() time.Time) func() time.Time {
	if now == nil {
		return func() time.Time { return time.Now().UTC() }
	}
	return now
}
