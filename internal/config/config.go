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
	// VerificationTTL is how long an email verification link stays valid. Longer than a
	// reset: it proves control of an address rather than authorising a password change.
	VerificationTTL time.Duration
}

// RateLimit bounds request rates, in two groups that exist for different reasons.
//
// Attempts are about guessing: a wrong password or a wrong token is a failed attempt, and
// the limit stays tight because the only thing that costs is a brute-force search.
//
// Actions are about abuse: registering, and sending mail. These are not failures, so their
// limit is generous — several people behind one address, or one person who mistyped their
// email twice, must not lock anyone out. The point is only to stop bulk abuse.
//
// Keeping them separate matters. A single shared bucket means ordinary use spends the
// same allowance as an attack, which is how a handful of sign-ups and resends cut off
// registration for everyone on that address.
//
// A zero Failures value disables a limit.
type RateLimit struct {
	// Attempts.
	Login         Limit
	ResetPassword Limit
	VerifyEmail   Limit
	// Actions.
	Register           Limit
	ForgotPassword     Limit
	ResendVerification Limit
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
			// --- Attempts: tight, because the cost of a wrong guess is only a wrong
			// guess. ---

			// Five wrong passwords per account or address per window. Generous for a
			// person, useless for guessing.
			Login: Limit{Failures: envIntOr("RATE_LIMIT_LOGIN_FAILURES", 5), Window: window},
			// Guessing a reset token is hopeless anyway; this only bounds the noise.
			ResetPassword: Limit{Failures: envIntOr("RATE_LIMIT_RESET_PASSWORD_FAILURES", 10), Window: window},
			// Verification links are not secret in the way a reset link is, so this only
			// needs to stop grinding.
			VerifyEmail: Limit{Failures: envIntOr("RATE_LIMIT_VERIFY_EMAIL_FAILURES", 10), Window: window},

			// --- Actions: generous, because a legitimate attempt is not a failure. ---

			// Counted on every attempt, successful or not, since account creation is what
			// is being bounded. Was 3, which three real sign-ups from one address would
			// exhaust — and which then blocked that address from registering at all until
			// the window passed.
			Register: Limit{Failures: envIntOr("RATE_LIMIT_REGISTER_FAILURES", 10), Window: window},
			// Sending mail. Was 3, which a person who mistyped their address twice would
			// use up while doing nothing wrong.
			ForgotPassword: Limit{Failures: envIntOr("RATE_LIMIT_FORGOT_PASSWORD_FAILURES", 10), Window: window},
			ResendVerification: Limit{
				Failures: envIntOr("RATE_LIMIT_RESEND_VERIFICATION_FAILURES", 20),
				Window:   window,
			},
		},
		// Off by default: X-Forwarded-For is client-supplied and must only be
		// trusted when a proxy is known to overwrite it.
		TrustProxyHeaders: os.Getenv("TRUST_PROXY_HEADERS") == "true",

		Mail: Mail{
			Provider: strings.ToLower(envOr("MAIL_PROVIDER", "stub")),
			APIKey:   os.Getenv("RESEND_API_KEY"),
			// Deliberately no From default. The address must sit on a domain that is
			// verified with the provider, and only the deployment knows which one
			// that is; a plausible-looking default on an unverified domain would be
			// accepted here and then rejected by the provider at the first send. The
			// mailtest command refuses to run when this is unset, so the mistake
			// surfaces immediately instead of silently.
			From:          os.Getenv("MAIL_FROM"),
			ReplyTo:       os.Getenv("MAIL_REPLY_TO"),
			PublicSiteURL: strings.TrimRight(envOr("PUBLIC_SITE_URL", "https://couchcollective.software"), "/"),
			ResetTTL:      time.Duration(envIntOr("PASSWORD_RESET_TTL_MINUTES", 30)) * time.Minute,
			VerificationTTL: time.Duration(
				envIntOr("EMAIL_VERIFICATION_TTL_HOURS", 24)) * time.Hour,
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
