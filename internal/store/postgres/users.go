package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store"
)

func (s *Store) CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, password_hash, role, is_agent, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+userColumns,
		u.ID, u.Email, passwordHash, string(u.Role), u.IsAgent, u.CreatedAt,
	)
	created, err := scanUser(row)
	if err != nil {
		if isPgCode(err, "23505") {
			return domain.User{}, apierror.Conflict("Email already in use")
		}
		return domain.User{}, mapPgError(err)
	}
	return created, nil
}

func (s *Store) GetUserByID(ctx context.Context, id uuid.UUID) (domain.User, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apierror.NotFound("User not found")
		}
		return domain.User{}, mapPgError(err)
	}
	return u, nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (domain.User, string, error) {
	u, hash, err := scanUserWithHash(s.pool.QueryRow(ctx,
		`SELECT `+userColumnsWithHash+` FROM users WHERE email = $1`,
		email,
	))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, "", apierror.NotFound("User not found")
		}
		return domain.User{}, "", mapPgError(err)
	}
	return u, hash, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+userColumns+` FROM users ORDER BY created_at`)
	if err != nil {
		return nil, mapPgError(err)
	}
	defer rows.Close()

	users := make([]domain.User, 0)
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, mapPgError(err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError(err)
	}
	return users, nil
}

// DeleteUser removes the user row outright.
//
// A user's appointments are deliberately not touched: appointments carry the
// owner's email so they stay meaningful, and migration 0003 drops the foreign keys
// into users so this delete cannot be blocked by them. Any 23503 here would mean
// those constraints are still in place, so it is surfaced rather than masked.
func (s *Store) DeleteUser(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	if err != nil {
		return mapPgError(err)
	}
	if tag.RowsAffected() == 0 {
		return apierror.NotFound("User not found")
	}
	return nil
}

func (s *Store) UpdateUser(ctx context.Context, id uuid.UUID, role *domain.Role, isAgent *bool) (domain.User, error) {
	var roleStr *string
	if role != nil {
		v := string(*role)
		roleStr = &v
	}

	row := s.pool.QueryRow(ctx,
		`UPDATE users
		 SET role = COALESCE($2, role), is_agent = COALESCE($3, is_agent)
		 WHERE id = $1
		 RETURNING `+userColumns,
		id, roleStr, isAgent,
	)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apierror.NotFound("User not found")
		}
		return domain.User{}, mapPgError(err)
	}
	return u, nil
}

// UpdateUserProfile updates the caller's own profile fields.
//
// Every field is a pointer so "absent from the request" (nil) can be told apart
// from "cleared" (a pointer to an empty value). COALESCE leaves an absent field
// alone, and a supplied empty string is stored as-is. A stored empty string and a
// stored null are equivalent on the wire: scanUser normalises both to a nil
// pointer, so a reader always sees "not set" as null. Normalising here instead
// would collapse "unchanged" onto "cleared", which is the bug this avoids.
//
// Birthday month and day move together: the request layer pairs the values, and a
// database constraint enforces that they are both set or both null.
func (s *Store) UpdateUserProfile(ctx context.Context, id uuid.UUID, p store.ProfileUpdate) (domain.User, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE users
		 SET display_name = COALESCE($2, display_name),
		     birth_month  = COALESCE($3, birth_month),
		     birth_day    = COALESCE($4, birth_day),
		     pronouns     = COALESCE($5, pronouns),
		     job_title    = COALESCE($6, job_title),
		     bio          = COALESCE($7, bio)
		 WHERE id = $1
		 RETURNING `+userColumns,
		id, p.DisplayName, p.BirthMonth, p.BirthDay, p.Pronouns, p.JobTitle, p.Bio,
	)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, apierror.NotFound("User not found")
		}
		return domain.User{}, mapPgError(err)
	}
	return u, nil
}
