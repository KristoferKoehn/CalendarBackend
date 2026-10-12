//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/httpapi"
	"kristoferkoehn/CalendarBackend/internal/mcp"
	"kristoferkoehn/CalendarBackend/internal/migrations"
	"kristoferkoehn/CalendarBackend/internal/store/postgres"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		os.Stderr.WriteString("TEST_DATABASE_URL not set; skipping integration tests\n")
		os.Exit(0)
	}
	if !isTestDatabase(dbURL) {
		os.Stderr.WriteString("integration tests require a database name ending in _test\n")
		os.Exit(1)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		panic(err)
	}
	defer pool.Close()

	if err := migrations.Migrate(ctx, pool); err != nil {
		panic(err)
	}

	testPool = pool
	truncate(ctx)

	code := m.Run()

	truncate(ctx)

	os.Exit(code)
}

func isTestDatabase(rawURL string) bool {
	cfg, err := pgxpool.ParseConfig(rawURL)
	if err != nil {
		return false
	}
	return strings.HasSuffix(cfg.ConnConfig.Database, "_test")
}

func truncate(ctx context.Context) {
	_, _ = testPool.Exec(ctx, `TRUNCATE appointments, sessions, resources, users CASCADE`)
}

func truncateAll(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `TRUNCATE appointments, sessions, resources, users CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

func newStore() *postgres.Store {
	return postgres.New(testPool)
}

func newServer(t *testing.T) http.Handler {
	t.Helper()
	st := newStore()

	authSvc := core.NewAuthService(st, st, time.Hour, nil)
	resourceSvc := core.NewResourceService(st)
	userSvc := core.NewUserService(st, st, st)
	appointmentSvc := core.NewAppointmentService(st, st, nil)

	rest := httpapi.NewHandler(httpapi.Deps{
		Auth:         authSvc,
		Resources:    resourceSvc,
		Users:        userSvc,
		Appointments: appointmentSvc,
		Sessions:     st,
		UserStore:    st,
		CORSOrigins:  []string{"http://localhost:3000"},
	})

	mcpHandler := mcp.NewHandler(mcp.Deps{
		Auth:         authSvc,
		Resources:    resourceSvc,
		Appointments: appointmentSvc,
		Sessions:     st,
		UserStore:    st,
	})

	top := http.NewServeMux()
	top.Handle("/mcp", mcpHandler)
	top.Handle("/", rest)
	return top
}
