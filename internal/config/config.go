// Package config loads server configuration from environment variables.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"kristoferkoehn/CalendarBackend/internal/ratelimit"
)

// Config holds runtime settings for the server.
type Config struct {
	DatabaseURL string
	Port        string
	TokenTTL    time.Duration
	CORSOrigins []string
	Seed        bool

	// Rate limiting for the endpoints that can be abused.
	RateLimit         RateLimit
	TrustProxyHeaders bool

	// Mail describes how to send transactional email, and where the links inside
	// those emails should point.
	Mail Mail
}

// Mail is the outbound email configuration.
//
// Provider defaults to "stub", which records messages instead of sending them. A
// deployment that sets a real provider is making an explicit choice; nothing sends
// by accident.
type Mail struct {
	// Provider is "stub" or "resend".
	Provider string
	// APIKey is the provider secret. Never logged.
	APIKey string
	// From is the envelope sender, e.g. "CalendarSite <no-reply@example.com>".
	From string
	// ReplyTo is optional; when empty, replies go to From.
	ReplyTo string
	// PublicSiteURL is the address of the web app, used to build links. It must be
	// the public origin with no port, because these links are opened from a mail
	// client rather than from the browser that is talking to the API.
	PublicSiteURL string
	// ResetTTL is how long a password reset link stays valid.
	ResetTTL time.Duration
}

// RateLimit bounds how many failed attempts are tolerated per caller and per
// account within a window. A zero Failures value disables that limit.
type RateLimit struct {
	Login    Limit
	Register Limit
	// ForgotPassword gates the reset-request endpoint, which sends mail on success.
	ForgotPassword Limit
	// ResetPassword is caller-scoped only: there is no account to key on until the
	// token is inspected.
	ResetPassword Limit
}

// Limit is the number of failures tolerated within a window.
type Limit struct {
	Failures int
	Window   time.Duration
}

// ToLimit converts to the limiter's own type, keeping the ratelimit package free
// of any dependency on configuration.
func (l Limit) ToLimit() ratelimit.Limit {
	return ratelimit.Limit{Failures: l.Failures, Window: l.Window}
}

// Load reads configuration from the environment, applying defaults.
func Load() Config {
	window := time.Duration(envIntOr("RATE_LIMIT_WINDOW_SECONDS", 900)) * time.Second

	return Config{
		DatabaseURL: os.Getenv("DATABASE_URL"),
		Port:        envOr("PORT", "4001"),
		TokenTTL:    time.Duration(envIntOr("TOKEN_TTL_HOURS", 24)) * time.Hour,
		CORSOrigins: corsOrigins(),
		Seed:        os.Getenv("SEED") == "true",

		RateLimit: RateLimit{
			// Five wrong passwords per account or address per window. Generous for
			// a person, useless for guessing.
			Login: Limit{Failures: envIntOr("RATE_LIMIT_LOGIN_FAILURES", 5), Window: window},
			// Creating accounts is the more expensive action, so it is tighter.
			Register: Limit{Failures: envIntOr("RATE_LIMIT_REGISTER_FAILURES", 3), Window: window},
			// Sending mail is expensive and abusable, so this is tight: three reset
			// emails per address per window.
			ForgotPassword: Limit{Failures: envIntOr("RATE_LIMIT_FORGOT_PASSWORD_FAILURES", 3), Window: window},
			// Guessing a reset token is hopeless anyway; this only bounds the noise.
			ResetPassword: Limit{Failures: envIntOr("RATE_LIMIT_RESET_PASSWORD_FAILURES", 10), Window: window},
		},
		// Off by default: X-Forwarded-For is client-supplied and must only be
		// trusted when a proxy is known to overwrite it.
		TrustProxyHeaders: os.Getenv("TRUST_PROXY_HEADERS") == "true",

		Mail: Mail{
			Provider:      strings.ToLower(envOr("MAIL_PROVIDER", "stub")),
			APIKey:        os.Getenv("RESEND_API_KEY"),
			From:          envOr("MAIL_FROM", "CalendarSite <no-reply@couchcollective.software>"),
			ReplyTo:       os.Getenv("MAIL_REPLY_TO"),
			PublicSiteURL: strings.TrimRight(envOr("PUBLIC_SITE_URL", "https://couchcollective.software"), "/"),
			ResetTTL:      time.Duration(envIntOr("PASSWORD_RESET_TTL_MINUTES", 30)) * time.Minute,
		},
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return fallback
}

func corsOrigins() []string {
	raw := os.Getenv("CORS_ORIGINS")
	if raw == "" {
		return []string{
			"https://couchcollective.software",
		}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
