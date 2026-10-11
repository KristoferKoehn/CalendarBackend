package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func (s *Store) CreateUser(ctx context.Context, u domain.User, passwordHash string) (domain.User, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO users (id, email, password_hash, role, is_agent, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, email, role, is_agent, created_at`,
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
		`SELECT id, email, role, is_agent, created_at FROM users WHERE id = $1`, id)
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
	var u domain.User
	var role, passwordHash string
	err := s.pool.QueryRow(ctx,
		`SELECT id, email, password_hash, role, is_agent, created_at FROM users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.Email, &passwordHash, &role, &u.IsAgent, &u.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.User{}, "", apierror.NotFound("User not found")
		}
		return domain.User{}, "", mapPgError(err)
	}
	u.Role = domain.Role(role)
	u.CreatedAt = u.CreatedAt.UTC()
	return u, passwordHash, nil
}

func (s *Store) ListUsers(ctx context.Context) ([]domain.User, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, email, role, is_agent, created_at FROM users ORDER BY created_at`)
	if err != nil {
		return nil, mapPgError(err)
	}
	defer rows.Close()

	var users []domain.User
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
		 RETURNING id, email, role, is_agent, created_at`,
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
