// Package domain holds the core data types shared by services and stores.
package domain

import (
	"time"

	"github.com/google/uuid"
)

// Role is the authorization role on a user.
type Role string

const (
	RoleUser  Role = "user"
	RoleAdmin Role = "admin"
)

// AppointmentStatus is the lifecycle state of an appointment.
type AppointmentStatus string

const (
	StatusBooked    AppointmentStatus = "booked"
	StatusCancelled AppointmentStatus = "cancelled"
)

// User is the public user shape from the contract.
//
// Every field above is visible to all authenticated users, because profiles are a
// shared directory. The profile fields are optional and self-service: a user edits
// their own via PATCH /auth/me.
//
// Birthday is deliberately month and day only — no year — so the system can
// recognise a birthday without holding a full date of birth.
type User struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName *string   `json:"displayName"`
	BirthMonth  *int      `json:"birthMonth"`
	BirthDay    *int      `json:"birthDay"`
	Pronouns    *string   `json:"pronouns"`
	JobTitle    *string   `json:"jobTitle"`
	Bio         *string   `json:"bio"`
	Role        Role      `json:"role"`
	IsAgent     bool      `json:"isAgent"`
	// EmailVerifiedAt is null until the address is proven. Accounts created before
	// verification existed are backfilled by migration 0005, so a null here means
	// a genuine unverified signup rather than an oversight.
	EmailVerifiedAt *time.Time `json:"emailVerifiedAt"`
	CreatedAt       time.Time  `json:"createdAt"`
}

// Label is the human-readable name for a user, falling back to the email when no
// display name is set. Used wherever a person is shown to someone else.
func (u User) Label() string {
	if u.DisplayName != nil && *u.DisplayName != "" {
		return *u.DisplayName
	}
	return u.Email
}

// OwnerLabel is the name to show for an appointment's owner, falling back to the
// email label captured at booking time.
func (a Appointment) OwnerLabel() string {
	if a.OwnerName != nil && *a.OwnerName != "" {
		return *a.OwnerName
	}
	return a.OwnerEmail
}

// Resource is a bookable resource. Description is nullable in responses.
type Resource struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description *string   `json:"description"`
	Timezone    string    `json:"timezone"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
}

// Appointment is the public appointment shape from the contract.
// CancelledAt is nullable in responses.
//
// OwnerEmail is denormalised from the owning user at creation so an appointment
// stays meaningful after its owner is deleted. UserID is a historical reference
// and may point at a user that no longer exists.
type Appointment struct {
	ID           uuid.UUID         `json:"id"`
	UserID       uuid.UUID         `json:"userId"`
	OwnerEmail   string            `json:"ownerEmail"`
	OwnerName    *string           `json:"ownerName"`
	ResourceID   uuid.UUID         `json:"resourceId"`
	Title        string            `json:"title"`
	Description  string            `json:"description"`
	StartAt      time.Time         `json:"startAt"`
	EndAt        time.Time         `json:"endAt"`
	StartAtLocal string            `json:"startAtLocal"`
	EndAtLocal   string            `json:"endAtLocal"`
	Status       AppointmentStatus `json:"status"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
	CancelledAt  *time.Time        `json:"cancelledAt"`
}

// Session is internal persistence state; it is never serialized to clients.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash string
	ExpiresAt time.Time
}

// TokenPurpose distinguishes what an auth token authorises. The values match the
// CHECK constraint on auth_tokens.purpose.
type TokenPurpose string

const (
	// PurposeEmailVerification proves control of an email address.
	PurposeEmailVerification TokenPurpose = "email_verification"
	// PurposePasswordReset authorises setting a new password.
	PurposePasswordReset TokenPurpose = "password_reset"
)

// AuthToken is a single-use, hashed token. Internal persistence state: the raw
// token is never stored, so it is never serialized to clients either.
type AuthToken struct {
	ID         uuid.UUID
	UserID     uuid.UUID
	Purpose    TokenPurpose
	TokenHash  string
	ExpiresAt  time.Time
	ConsumedAt *time.Time
	CreatedAt  time.Time
}

// Usable reports whether the token may still be redeemed at the given time.
func (t AuthToken) Usable(now time.Time) bool {
	return t.ConsumedAt == nil && now.Before(t.ExpiresAt)
}
