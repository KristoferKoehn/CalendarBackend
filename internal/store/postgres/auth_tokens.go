package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

// authTokenColumns is shared by every read so the column order and the scan
// destinations cannot drift apart.
const authTokenColumns = `id, user_id, purpose, token_hash, expires_at, consumed_at, created_at`

func scanAuthToken(row pgx.Row) (domain.AuthToken, error) {
	var t domain.AuthToken
	if err := row.Scan(&t.ID, &t.UserID, &t.Purpose, &t.TokenHash, &t.ExpiresAt, &t.ConsumedAt, &t.CreatedAt); err != nil {
		return domain.AuthToken{}, err
	}
	return t, nil
}

func (s *Store) CreateAuthToken(ctx context.Context, t domain.AuthToken) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_tokens (id, user_id, purpose, token_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.UserID, t.Purpose, t.TokenHash, t.ExpiresAt, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("create auth token: %w", err)
	}
	return nil
}

func (s *Store) GetAuthTokenByHash(ctx context.Context, tokenHash string) (domain.AuthToken, error) {
	t, err := scanAuthToken(s.pool.QueryRow(ctx,
		`SELECT `+authTokenColumns+` FROM auth_tokens WHERE token_hash = $1`, tokenHash))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.AuthToken{}, apierror.NotFound("token not found")
		}
		return domain.AuthToken{}, fmt.Errorf("get auth token: %w", err)
	}
	return t, nil
}

// ConsumeAuthToken marks the token used and returns it, but only when it is still
// live. The condition lives in the WHERE clause so the check and the update are a
// single statement: two concurrent requests cannot both succeed, because the second
// sees no matching row.
func (s *Store) ConsumeAuthToken(ctx context.Context, tokenHash string, now time.Time) (domain.AuthToken, error) {
	t, err := scanAuthToken(s.pool.QueryRow(ctx, `
		UPDATE auth_tokens
		SET consumed_at = $2
		WHERE token_hash = $1
		  AND consumed_at IS NULL
		  AND expires_at > $2
		RETURNING `+authTokenColumns, tokenHash, now))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Either unknown, already used, or expired. The caller reports one
			// message for all three so a wrong guess learns nothing.
			return domain.AuthToken{}, apierror.NotFound("token is not valid")
		}
		return domain.AuthToken{}, fmt.Errorf("consume auth token: %w", err)
	}
	return t, nil
}

func (s *Store) DeleteUserAuthTokens(ctx context.Context, userID uuid.UUID, purpose *domain.TokenPurpose) error {
	// One statement with a null-tolerant predicate, rather than two branches, so the
	// two cases cannot drift apart.
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM auth_tokens
		WHERE user_id = $1
		  AND ($2::text IS NULL OR purpose = $2)`, userID, purpose); err != nil {
		return fmt.Errorf("delete auth tokens: %w", err)
	}
	return nil
}
