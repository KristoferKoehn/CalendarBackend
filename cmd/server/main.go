package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"

	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/httpapi"
	"kristoferkoehn/CalendarBackend/internal/mcp"
	"kristoferkoehn/CalendarBackend/internal/migrations"
	"kristoferkoehn/CalendarBackend/internal/seed"
	"kristoferkoehn/CalendarBackend/internal/store/postgres"
)

func main() {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}

	switch command {
	case "migrate":
		runMigrations()
	case "seed":
		runSeed()
	case "serve":
		runServe()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(2)
	}
}

func connect(ctx context.Context) *pgxpool.Pool {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(os.Stderr, "DATABASE_URL is required")
		os.Exit(1)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	return pool
}

func runMigrations() {
	ctx := context.Background()
	pool := connect(ctx)
	defer pool.Close()

	if err := migrations.Migrate(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("migrations applied")
}

func runSeed() {
	ctx := context.Background()
	pool := connect(ctx)
	defer pool.Close()

	if err := migrations.Migrate(ctx, pool); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(1)
	}

	st := postgres.New(pool)
	if err := seed.Run(ctx, st, st); err != nil {
		fmt.Fprintf(os.Stderr, "seed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("seed complete")
}

func runServe() {
	cfg := config.Load()
	ctx := context.Background()
	pool := connect(ctx)
	defer pool.Close()

	if err := migrations.Migrate(ctx, pool); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	st := postgres.New(pool)
	authSvc := core.NewAuthService(st, st, cfg.TokenTTL, nil)
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
		CORSOrigins:  cfg.CORSOrigins,
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

	addr := ":" + cfg.Port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, top); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
