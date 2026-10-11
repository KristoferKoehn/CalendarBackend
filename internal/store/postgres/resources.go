package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func (s *Store) CreateResource(ctx context.Context, r domain.Resource) (domain.Resource, error) {
	row := s.pool.QueryRow(ctx,
		`INSERT INTO resources (id, name, description, timezone, active, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING id, name, description, timezone, active, created_at`,
		r.ID, r.Name, r.Description, r.Timezone, r.Active, r.CreatedAt,
	)
	created, err := scanResource(row)
	if err != nil {
		return domain.Resource{}, mapPgError(err)
	}
	return created, nil
}

func (s *Store) GetResourceByID(ctx context.Context, id uuid.UUID) (domain.Resource, error) {
	row := s.pool.QueryRow(ctx,
		`SELECT id, name, description, timezone, active, created_at FROM resources WHERE id = $1`, id)
	r, err := scanResource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Resource{}, apierror.NotFound("Resource not found")
		}
		return domain.Resource{}, mapPgError(err)
	}
	return r, nil
}

func (s *Store) ListActiveResources(ctx context.Context) ([]domain.Resource, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, description, timezone, active, created_at
		 FROM resources WHERE active = true ORDER BY created_at`)
	if err != nil {
		return nil, mapPgError(err)
	}
	defer rows.Close()

	var list []domain.Resource
	for rows.Next() {
		r, err := scanResource(rows)
		if err != nil {
			return nil, mapPgError(err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, mapPgError(err)
	}
	return list, nil
}

func (s *Store) UpdateResource(ctx context.Context, r domain.Resource) (domain.Resource, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE resources
		 SET name = $2, description = $3, timezone = $4, active = $5
		 WHERE id = $1
		 RETURNING id, name, description, timezone, active, created_at`,
		r.ID, r.Name, r.Description, r.Timezone, r.Active,
	)
	updated, err := scanResource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Resource{}, apierror.NotFound("Resource not found")
		}
		return domain.Resource{}, mapPgError(err)
	}
	return updated, nil
}

func (s *Store) DeactivateResource(ctx context.Context, id uuid.UUID) (domain.Resource, error) {
	row := s.pool.QueryRow(ctx,
		`UPDATE resources SET active = false WHERE id = $1
		 RETURNING id, name, description, timezone, active, created_at`, id)
	r, err := scanResource(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Resource{}, apierror.NotFound("Resource not found")
		}
		return domain.Resource{}, mapPgError(err)
	}
	return r, nil
}
