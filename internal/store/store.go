// Package store defines the persistence boundary that core services depend on.
// The PostgreSQL adapter and in-memory fakes both implement these interfaces.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/domain"
)

// UserStore persists users and their password hashes.
type UserStore interface {
	CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (domain.User, string, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	UpdateUser(ctx context.Context, id uuid.UUID, role *domain.Role, isAgent *bool) (domain.User, error)
}

// ResourceStore persists bookable resources.
type ResourceStore interface {
	CreateResource(ctx context.Context, r domain.Resource) (domain.Resource, error)
	GetResourceByID(ctx context.Context, id uuid.UUID) (domain.Resource, error)
	ListActiveResources(ctx context.Context) ([]domain.Resource, error)
	UpdateResource(ctx context.Context, r domain.Resource) (domain.Resource, error)
	DeactivateResource(ctx context.Context, id uuid.UUID) (domain.Resource, error)
}

// AppointmentStore persists appointments and overlap checks.
type AppointmentStore interface {
	CreateAppointment(ctx context.Context, a domain.Appointment) (domain.Appointment, error)
	GetAppointmentByID(ctx context.Context, id uuid.UUID) (domain.Appointment, error)
	ListAppointments(ctx context.Context, f AppointmentFilter) ([]domain.Appointment, error)
	CancelAppointment(ctx context.Context, id uuid.UUID, cancelledAt time.Time) (domain.Appointment, error)
	RescheduleAppointment(ctx context.Context, p RescheduleParams) (domain.Appointment, error)
	HasOverlap(ctx context.Context, resourceID uuid.UUID, start, end time.Time, excludeID uuid.UUID) (bool, error)
}

// SessionStore persists opaque bearer token sessions.
type SessionStore interface {
	CreateSession(ctx context.Context, s domain.Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error)
}

// AppointmentFilter carries optional list filters. From and To are UTC bounds.
type AppointmentFilter struct {
	ResourceID *uuid.UUID
	UserID     *uuid.UUID
	From       *time.Time
	To         *time.Time
	Status     *domain.AppointmentStatus
}

// RescheduleParams carries the fields for a reschedule update.
type RescheduleParams struct {
	ID        uuid.UUID
	Start     time.Time
	End       time.Time
	UpdatedAt time.Time
}
