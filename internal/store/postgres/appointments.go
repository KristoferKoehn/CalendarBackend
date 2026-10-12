package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

func (s *Store) CreateAppointment(ctx context.Context, a domain.Appointment) (domain.Appointment, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO appointments (id, user_id, resource_id, title, description, start_at, end_at, status, created_at, updated_at, cancelled_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 RETURNING id, user_id, resource_id, title, description, start_at, end_at, status, created_at, updated_at, cancelled_at`,
		a.ID, a.UserID, a.ResourceID, a.Title, a.Description, a.StartAt, a.EndAt, string(a.Status), a.CreatedAt, a.UpdatedAt, a.CancelledAt,
	)
	created, err := scanAppointment(row)
	if err != nil {
		return domain.Appointment{}, mapPgError(err)
	}
	return created, nil
}

func (s *Store) GetAppointmentByID(ctx context.Context, id uuid.UUID) (domain.Appointment, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, user_id, resource_id, title, description, start_at, end_at, status, created_at, updated_at, cancelled_at
		 FROM appointments WHERE id = $1`, id)
	a, err := scanAppointment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Appointment{}, apierror.NotFound("Appointment not found")
		}
		return domain.Appointment{}, mapPgError(err)
	}
	return a, nil
}

func (s *Store) ListAppointments(ctx context.Context, f store.AppointmentFilter) ([]domain.Appointment, error) {
	query := `SELECT id, user_id, resource_id, title, description, start_at, end_at, status, created_at, updated_at, cancelled_at FROM appointments`
	var conds []string
	var args []any

	arg := func(v any) string {
		args = append(args, v)
		return fmt.Sprintf("$%d", len(args))
	}

	if f.ResourceID != nil {
		conds = append(conds, "resource_id = "+arg(*f.ResourceID))
	}
	if f.UserID != nil {
		conds = append(conds, "user_id = "+arg(*f.UserID))
	}
	if f.Title != nil {
		conds = append(conds, "strpos(lower(title), lower("+arg(*f.Title)+")) > 0")
	}
	if f.Status != nil {
		conds = append(conds, "status = "+arg(string(*f.Status)))
	}
	if f.From != nil {
		conds = append(conds, "start_at >= "+arg(*f.From))
	}
	if f.To != nil {
		conds = append(conds, "start_at < "+arg(*f.To))
	}
	if len(conds) > 0 {
		query += " WHERE " + strings.Join(conds, " AND ")
	}
	query += " ORDER BY start_at"

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, mapPgError(err)
	}
	defer rows.Close()

	list := make([]domain.Appointment, 0)
	for rows.Next() {
		a, err := scanAppointment(rows)
		if err != nil {
			return nil, mapPgError(err)
		}
		list = append(list, a)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError(err)
	}
	return list, nil
}

func (s *Store) CancelAppointment(ctx context.Context, id uuid.UUID, cancelledAt time.Time) (domain.Appointment, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE appointments
		 SET status = 'cancelled', cancelled_at = $2, updated_at = $2
		 WHERE id = $1
		 RETURNING id, user_id, resource_id, title, description, start_at, end_at, status, created_at, updated_at, cancelled_at`,
		id, cancelledAt,
	)
	a, err := scanAppointment(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Appointment{}, apierror.NotFound("Appointment not found")
		}
		return domain.Appointment{}, mapPgError(err)
	}
	return a, nil
}

func (s *Store) RescheduleAppointment(ctx context.Context, p store.RescheduleParams) (domain.Appointment, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Appointment{}, mapPgError(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Temporarily cancel the row so the exclusion constraint does not compare
	// the new range against its own old range.
	tag, err := tx.Exec(ctx,
		`UPDATE appointments SET status = 'cancelled', cancelled_at = $2, updated_at = $2
		 WHERE id = $1 AND status = 'booked'`,
		p.ID, p.UpdatedAt,
	)
	if err != nil {
		return domain.Appointment{}, mapPgError(err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Appointment{}, apierror.NotFound("Appointment not found")
	}

	row := tx.QueryRow(ctx,
		`UPDATE appointments
		 SET title = $2, description = $3, start_at = $4, end_at = $5, status = 'booked', cancelled_at = NULL, updated_at = $6
		 WHERE id = $1
		 RETURNING id, user_id, resource_id, title, description, start_at, end_at, status, created_at, updated_at, cancelled_at`,
		p.ID, p.Title, p.Description, p.Start, p.End, p.UpdatedAt,
	)
	a, err := scanAppointment(row)
	if err != nil {
		return domain.Appointment{}, mapPgError(err)
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Appointment{}, mapPgError(err)
	}
	return a, nil
}

func (s *Store) HasOverlap(ctx context.Context, resourceID uuid.UUID, start, end time.Time, excludeID uuid.UUID) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM appointments
			WHERE resource_id = $1 AND status = 'booked'
			  AND start_at < $3 AND end_at > $2
			  AND id <> $4
		)`,
		resourceID, start, end, excludeID,
	).Scan(&exists)
	if err != nil {
		return false, mapPgError(err)
	}
	return exists, nil
}
