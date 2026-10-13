package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/httpapi"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/mcp"
	"kristoferkoehn/CalendarBackend/internal/migrations"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
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
	case "mailtest":
		runMailTest(os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", command)
		os.Exit(2)
	}
}

// newMailSender builds the configured sender.
//
// Provider "stub" is the default and sends nothing, so a missing or mistyped
// configuration cannot email anybody. Anything unrecognised is also a stub, but it
// says so loudly at startup.
func newMailSender(cfg config.Mail) mail.Sender {
	switch cfg.Provider {
	case "resend":
		return mail.NewResend(cfg.APIKey, cfg.From, cfg.ReplyTo)
	case "", "stub":
		return mail.NewStub()
	default:
		log.Printf("mail: unknown MAIL_PROVIDER %q; falling back to the stub sender", cfg.Provider)
		return mail.NewStub()
	}
}

// runMailTest sends one message to a given address so a provider, key and DNS setup
// can be checked without going through the product. It is a command rather than an
// endpoint on purpose: an HTTP route that sends arbitrary email is a spam relay
// waiting to be found.
func runMailTest(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: CalendarBackend.exe mailtest <to-address>")
		os.Exit(2)
	}
	to := args[0]

	cfg := config.Load()

	// Refuse to start when the provider is named but not usable. Failing here with a
	// clear message is far better than discovering it when someone requests a
	// password reset.
	if cfg.Mail.Provider == "resend" {
		var missing []string
		if strings.TrimSpace(cfg.Mail.APIKey) == "" {
			missing = append(missing, "RESEND_API_KEY")
		}
		if strings.TrimSpace(cfg.Mail.From) == "" {
			missing = append(missing, "MAIL_FROM")
		}
		if len(missing) > 0 {
			fmt.Fprintf(os.Stderr, "MAIL_PROVIDER=resend requires %s\n", strings.Join(missing, " and "))
			os.Exit(1)
		}
	} else {
		fmt.Fprintf(os.Stderr, "MAIL_PROVIDER is %q, so nothing would be sent.\n", cfg.Mail.Provider)
		fmt.Fprintln(os.Stderr, "Set MAIL_PROVIDER=resend and RESEND_API_KEY, then try again.")
		os.Exit(1)
	}

	sender := newMailSender(cfg.Mail)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	msg := mail.Message{
		To:      to,
		Subject: "CalendarSite mail test",
		Text: "This is a test message from CalendarSite.\n\n" +
			"If you are reading it, the provider accepted the send. Check the headers " +
			"to confirm SPF and DKIM passed; a message can arrive in the inbox while " +
			"still failing authentication.\n\n" +
			"Site: " + cfg.Mail.PublicSiteURL + "\n" +
			"Sender: " + cfg.Mail.From + "\n",
	}

	if err := sender.Send(ctx, msg); err != nil {
		fmt.Fprintf(os.Stderr, "mailtest failed via %s: %v\n", sender.Name(), err)
		var sendErr *mail.SendError
		if errors.As(err, &sendErr) && sendErr.Retryable() {
			fmt.Fprintln(os.Stderr, "This looks retryable (rate limited, a provider fault, or an unreachable host) rather than a configuration error.")
		}
		os.Exit(1)
	}
	fmt.Printf("mailtest: %s accepted a message to %s\n", sender.Name(), to)
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

	// One limiter shared by both transports, so sign-in allowances are drawn from
	// the same pool and neither transport can be used to bypass the other.
	limiter := ratelimit.New(cfg.RateLimit.Login.Window)
	defer limiter.Close()

	// Mail is built once and handed to both transports. The default stub records
	// instead of sending, so an unconfigured deployment cannot email anyone.
	mailer := newMailSender(cfg.Mail)
	log.Printf("mail: using %q sender, from %q, links point at %s",
		mailer.Name(), cfg.Mail.From, cfg.Mail.PublicSiteURL)

	// Password reset is built from the same stores the rest of the app uses, so a
	// reset invalidates live sessions exactly as a logout would.
	resetSvc := core.NewPasswordReset(st, st, st, mailer, cfg.Mail.PublicSiteURL, cfg.Mail.ResetTTL, nil)

	rest := httpapi.NewHandler(httpapi.Deps{
		Auth:              authSvc,
		Resources:         resourceSvc,
		Users:             userSvc,
		Appointments:      appointmentSvc,
		Sessions:          st,
		UserStore:         st,
		CORSOrigins:       cfg.CORSOrigins,
		Limiter:           limiter,
		RateLimit:         cfg.RateLimit,
		TrustProxyHeaders: cfg.TrustProxyHeaders,
		Mailer:            mailer,
		PublicSiteURL:     cfg.Mail.PublicSiteURL,
		PasswordReset:     resetSvc,
	})
	mcpHandler := mcp.NewHandler(mcp.Deps{
		Auth:              authSvc,
		Resources:         resourceSvc,
		Appointments:      appointmentSvc,
		Sessions:          st,
		UserStore:         st,
		Limiter:           limiter,
		RateLimit:         cfg.RateLimit,
		TrustProxyHeaders: cfg.TrustProxyHeaders,
	})

	top := http.NewServeMux()
	top.Handle("/mcp", mcpHandler)
	top.Handle("/", rest)

	addr := "127.0.0.1:" + cfg.Port
	log.Printf("listening on %s", addr)
	if err := http.ListenAndServe(addr, top); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
