package postgres

import (
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/timeutil"
)

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (domain.User, error) {
	var u domain.User
	var role string
	if err := s.Scan(&u.ID, &u.Email, &role, &u.IsAgent, &u.CreatedAt); err != nil {
		return u, err
	}
	u.Role = domain.Role(role)
	u.CreatedAt = u.CreatedAt.UTC()
	return u, nil
}

func scanResource(s scanner) (domain.Resource, error) {
	var r domain.Resource
	if err := s.Scan(&r.ID, &r.Name, &r.Description, &r.Timezone, &r.Active, &r.CreatedAt); err != nil {
		return r, err
	}
	r.CreatedAt = r.CreatedAt.UTC()
	return r, nil
}

// appointmentColumns is the shared SELECT/RETURNING list for an appointment.
// It must stay in sync with scanAppointment.
const appointmentColumns = `id, user_id, owner_email, resource_id, title, description,
	start_at, end_at, status, created_at, updated_at, cancelled_at`

func scanAppointment(s scanner) (domain.Appointment, error) {
	var a domain.Appointment
	var status string
	if err := s.Scan(
		&a.ID, &a.UserID, &a.OwnerEmail, &a.ResourceID,
		&a.Title, &a.Description,
		&a.StartAt, &a.EndAt, &status,
		&a.CreatedAt, &a.UpdatedAt, &a.CancelledAt,
	); err != nil {
		return a, err
	}
	a.Status = domain.AppointmentStatus(status)
	a.StartAt = a.StartAt.UTC()
	a.EndAt = a.EndAt.UTC()
	a.CreatedAt = a.CreatedAt.UTC()
	a.UpdatedAt = a.UpdatedAt.UTC()
	if a.CancelledAt != nil {
		t := a.CancelledAt.UTC()
		a.CancelledAt = &t
	}
	a.StartAtLocal = timeutil.FormatLocal(a.StartAt)
	a.EndAtLocal = timeutil.FormatLocal(a.EndAt)
	return a, nil
}
