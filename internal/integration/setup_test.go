//go:build integration

package integration

import (
	"context"
	"net/http"
	"os"
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
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		os.Stderr.WriteString("DATABASE_URL not set; skipping integration tests\n")
		os.Exit(0)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
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
