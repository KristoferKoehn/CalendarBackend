// Package postgres implements the store interfaces against PostgreSQL via pgx.
package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"kristoferkoehn/CalendarBackend/internal/store"
)

// Store is the PostgreSQL implementation of the persistence boundary.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by the given pool.
func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

var (
	_ store.UserStore        = (*Store)(nil)
	_ store.ResourceStore    = (*Store)(nil)
	_ store.AppointmentStore = (*Store)(nil)
	_ store.SessionStore     = (*Store)(nil)
)
