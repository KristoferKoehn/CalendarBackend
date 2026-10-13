// Package store defines the persistence boundary that core services depend on.
// The PostgreSQL adapter and in-memory fakes both implement these interfaces.
package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/domain"
)

// UserStore persists users, their password hashes, and their profile details.
type UserStore interface {
	CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error)
	GetUserByEmail(ctx context.Context, email string) (domain.User, string, error)
	ListUsers(ctx context.Context) ([]domain.User, error)
	UpdateUser(ctx context.Context, id uuid.UUID, role *domain.Role, isAgent *bool) (domain.User, error)
	UpdateUserProfile(ctx context.Context, id uuid.UUID, p ProfileUpdate) (domain.User, error)
	// UpdateUserPassword replaces the stored hash. Callers must invalidate existing
	// sessions separately; a reset that leaves old sessions alive is not a reset.
	UpdateUserPassword(ctx context.Context, id uuid.UUID, passwordHash string) error
	// MarkEmailVerified records the moment an address was proven.
	MarkEmailVerified(ctx context.Context, id uuid.UUID, at time.Time) error
	// SetEmailVerified sets or clears the verification stamp as an administrative act,
	// for when somebody cannot receive the mail — a mistyped address, or a provider that
	// refuses to deliver. Clearing it is the undo.
	SetEmailVerified(ctx context.Context, id uuid.UUID, verified bool, at time.Time) (domain.User, error)
	DeleteUser(ctx context.Context, id uuid.UUID) error
}

// AuthTokenStore persists single-use tokens for verification and password resets.
type AuthTokenStore interface {
	CreateAuthToken(ctx context.Context, t domain.AuthToken) error
	// GetAuthTokenByHash returns the token record for a hash. It does not check
	// expiry or consumption; the caller decides, so the reason for a refusal can be
	// distinguished and logged.
	GetAuthTokenByHash(ctx context.Context, tokenHash string) (domain.AuthToken, error)
	// ConsumeAuthToken marks a token used, returning the record only if it was still
	// live. The check and the write are one operation, so two simultaneous requests
	// cannot both redeem the same token.
	ConsumeAuthToken(ctx context.Context, tokenHash string, now time.Time) (domain.AuthToken, error)
	// DeleteUserAuthTokens removes a user's tokens, optionally limited to one
	// purpose. Used to invalidate outstanding links when a new one is issued.
	DeleteUserAuthTokens(ctx context.Context, userID uuid.UUID, purpose *domain.TokenPurpose) error
}

// ProfileUpdate carries the self-service profile fields a user may edit.
//
// Every field is a pointer so the store can tell "not supplied" (nil) from
// "cleared" (a pointer to an empty value). Birthday month and day are paired: the
// request layer sets both or neither.
type ProfileUpdate struct {
	DisplayName *string
	BirthMonth  *int
	BirthDay    *int
	Pronouns    *string
	JobTitle    *string
	Bio         *string
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
	DeleteAppointment(ctx context.Context, id uuid.UUID) error
	HasOverlap(ctx context.Context, resourceID uuid.UUID, start, end time.Time, excludeID uuid.UUID) (bool, error)
}

// SessionStore persists opaque bearer token sessions.
type SessionStore interface {
	CreateSession(ctx context.Context, s domain.Session) error
	GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error)
	DeleteSessionsByUser(ctx context.Context, userID uuid.UUID) error
}

// AppointmentFilter carries optional list filters. From and To are UTC bounds.
type AppointmentFilter struct {
	ResourceID *uuid.UUID
	UserID     *uuid.UUID
	Title      *string
	From       *time.Time
	To         *time.Time
	Status     *domain.AppointmentStatus
}

// RescheduleParams carries the fields for a reschedule update.
type RescheduleParams struct {
	ID          uuid.UUID
	Title       string
	Description string
	Start       time.Time
	End         time.Time
	UpdatedAt   time.Time
}
