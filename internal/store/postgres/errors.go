package postgres

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"

	"kristoferkoehn/CalendarBackend/internal/apierror"
)

// mapPgError translates PostgreSQL errors into contract errors.
func mapPgError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return apierror.Conflict("already exists")
		case "23P01": // exclusion_violation
			return apierror.Conflict("Overlapping booking")
		case "23503": // foreign_key_violation
			return apierror.NotFound("related record not found")
		case "23514": // check_violation
			return apierror.InvalidInput("invalid value")
		}
	}
	return err
}

func isPgCode(err error, code string) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == code
}
