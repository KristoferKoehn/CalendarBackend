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
type User struct {
	ID        uuid.UUID `json:"id"`
	Email     string    `json:"email"`
	Role      Role      `json:"role"`
	IsAgent   bool      `json:"isAgent"`
	CreatedAt time.Time `json:"createdAt"`
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
type Appointment struct {
	ID           uuid.UUID         `json:"id"`
	UserID       uuid.UUID         `json:"userId"`
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
