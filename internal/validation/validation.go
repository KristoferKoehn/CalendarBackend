// Package validation mirrors the contract's Zod schemas at input boundaries.
package validation

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var (
	dateRe  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	timeRe  = regexp.MustCompile(`^(?:[01]\d|2[0-3]):[0-5]\d$`)
	emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)
)

// Email trims and lowercases the address and checks its shape.
// It returns the normalized address.
func Email(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	if !emailRe.MatchString(e) {
		return "", errors.New("email must be a valid email address")
	}
	return e, nil
}

// Password requires at least 8 characters.
func Password(pw string) error {
	if len(pw) < 8 {
		return errors.New("password must be at least 8 characters")
	}
	return nil
}

// UUID requires a valid UUID.
func UUID(s string) error {
	if _, err := uuid.Parse(s); err != nil {
		return errors.New("must be a valid UUID")
	}
	return nil
}

// Date requires YYYY-MM-DD.
func Date(s string) error {
	if !dateRe.MatchString(s) {
		return errors.New("date must be YYYY-MM-DD")
	}
	return nil
}

// Time requires HH:mm in 24-hour format.
func Time(s string) error {
	if !timeRe.MatchString(s) {
		return errors.New("time must be HH:mm in 24-hour format")
	}
	return nil
}

// Role requires user or admin.
func Role(s string) error {
	if s != "user" && s != "admin" {
		return errors.New("role must be user or admin")
	}
	return nil
}

// AppointmentStatus requires booked or cancelled.
func AppointmentStatus(s string) error {
	if s != "booked" && s != "cancelled" {
		return errors.New("status must be booked or cancelled")
	}
	return nil
}

// TimeRange requires end strictly after start, both HH:mm.
func TimeRange(start, end string) error {
	startMin, err := toMinutes(start)
	if err != nil {
		return err
	}
	endMin, err := toMinutes(end)
	if err != nil {
		return err
	}
	if endMin <= startMin {
		return errors.New("endTime must be after startTime")
	}
	return nil
}

// Name requires 1 to 200 characters.
func Name(s string) error {
	if len(s) < 1 || len(s) > 200 {
		return errors.New("name must be between 1 and 200 characters")
	}
	return nil
}

// Timezone requires a non-empty timezone name.
func Timezone(s string) error {
	if strings.TrimSpace(s) == "" {
		return errors.New("timezone must not be empty")
	}
	return nil
}

func toMinutes(t string) (int, error) {
	if err := Time(t); err != nil {
		return 0, err
	}
	var h, m int
	if _, err := fmt.Sscanf(t, "%d:%d", &h, &m); err != nil {
		return 0, errors.New("time must be HH:mm in 24-hour format")
	}
	return h*60 + m, nil
}
