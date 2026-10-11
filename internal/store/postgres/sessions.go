package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"kristoferkoehn/CalendarBackend/internal/apierror"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

func (s *Store) CreateSession(ctx context.Context, sess domain.Session) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		sess.ID, sess.UserID, sess.TokenHash, sess.ExpiresAt,
	)
	return mapPgError(err)
}

func (s *Store) GetSessionByTokenHash(ctx context.Context, tokenHash string) (domain.Session, error) {
	var sess domain.Session
	err := s.pool.QueryRow(ctx,
		`SELECT id, user_id, token_hash, expires_at FROM sessions WHERE token_hash = $1`,
		tokenHash,
	).Scan(&sess.ID, &sess.UserID, &sess.TokenHash, &sess.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Session{}, apierror.NotFound("Session not found")
		}
		return domain.Session{}, mapPgError(err)
	}
	sess.ExpiresAt = sess.ExpiresAt.UTC()
	return sess, nil
}
