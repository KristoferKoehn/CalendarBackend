package migrations

import (
	"context"
	"embed"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed *.sql
var files embed.FS

// Migrate applies any embedded .sql files that are not yet recorded in
// schema_migrations. Each file runs in its own transaction.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`)
	if err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	var pending []string
	for _, name := range names {
		var applied bool
		err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`,
			name).Scan(&applied)
		if err != nil {
			return fmt.Errorf("check %s: %w", name, err)
		}
		if !applied {
			pending = append(pending, name)
		}
	}

	if len(pending) == 0 {
		return nil
	}

	conn, err := pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("acquire conn: %w", err)
	}
	defer conn.Release()

	for _, name := range pending {
		raw, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}

		// Simple protocol so one file can hold multiple statements.
		// The version literal is safe: names come from our own embed.FS.
		script := "BEGIN;\n" + string(raw) +
			"\nINSERT INTO schema_migrations (version) VALUES ('" + name + "');\nCOMMIT;\n"

		mrr := conn.Conn().PgConn().Exec(ctx, script)
		if _, err := mrr.ReadAll(); err != nil {
			_, _ = conn.Conn().PgConn().Exec(ctx, "ROLLBACK;").ReadAll()
			return fmt.Errorf("apply %s: %w", name, err)
		}
	}

	return nil
}
