package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

type verifyHTTPEnv struct {
	handler http.Handler
	users   *memory.UserStore
	mailer  *mail.Stub
}

// newVerifyHTTPEnv wires a handler with verification enabled, which newTestEnv leaves out
// so the other tests are unaffected.
func newVerifyHTTPEnv(t *testing.T, limits config.RateLimit) *verifyHTTPEnv {
	t.Helper()

	users := memory.NewUserStore()
	sessions := memory.NewSessionStore()
	tokens := memory.NewAuthTokenStore()
	resources := memory.NewResourceStore()
	appointments := memory.NewAppointmentStore()
	mailer := mail.NewStub()

	limiter := ratelimit.New(15 * time.Minute)
	t.Cleanup(limiter.Close)

	handler := NewHandler(Deps{
		Auth:              core.NewAuthService(users, sessions, time.Hour, nil),
		Resources:         core.NewResourceService(resources),
		Users:             core.NewUserService(users, appointments, sessions),
		Appointments:      core.NewAppointmentService(appointments, resources, nil),
		Sessions:          sessions,
		UserStore:         users,
		Limiter:           limiter,
		RateLimit:         limits,
		Mailer:            mailer,
		PublicSiteURL:     "https://example.test",
		EmailVerification: core.NewEmailVerification(users, tokens, mailer, "https://example.test", "CalendarSite <no-reply@verify.example.test>", core.DefaultVerificationTTL, nil),
	})

	return &verifyHTTPEnv{handler: handler, users: users, mailer: mailer}
}

func verifyLimits() config.RateLimit {
	return config.RateLimit{
		Login:              config.Limit{Failures: 50, Window: 15 * time.Minute},
		Register:           config.Limit{Failures: 50, Window: 15 * time.Minute},
		ForgotPassword:     config.Limit{Failures: 50, Window: 15 * time.Minute},
		ResetPassword:      config.Limit{Failures: 50, Window: 15 * time.Minute},
		VerifyEmail:        config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResendVerification: config.Limit{Failures: 3, Window: 15 * time.Minute},
	}
}

/** The raw token from the link in the most recent recorded message. */
func tokenFromLastVerificationMail(t *testing.T, mailer *mail.Stub) string {
	t.Helper()
	msg, ok := mailer.Last()
	if !ok {
		t.Fatal("no message was recorded")
	}
	const marker = "/verify-email?token="
	i := strings.Index(msg.Text, marker)
	if i < 0 {
		t.Fatalf("no verification link in:\n%s", msg.Text)
	}
	rest := msg.Text[i+len(marker):]
	if end := strings.IndexAny(rest, "\r\n "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestRegisterSendsAVerificationLink(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	sent := env.mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected exactly 1 message, got %d", len(sent))
	}
	if sent[0].To != "new@example.com" {
		t.Fatalf("mail went to %q", sent[0].To)
	}
}

// The two flows send from different domains on purpose, so a delivery problem with one
// cannot affect the other. Both are exercised in one run here, so a mix-up would show.
func TestEachFlowSendsFromItsOwnDomain(t *testing.T) {
	env := newResetHTTPEnv(t, resetLimits())

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "booker@example.com", "password": "password123",
	})
	doJSON(t, env.handler, http.MethodPost, "/auth/forgot-password", "", map[string]string{
		"email": "booker@example.com",
	})

	resetMsg, ok := env.mailer.Last()
	if !ok {
		t.Fatal("expected a reset message")
	}
	if !strings.Contains(resetMsg.Subject, "password") {
		t.Fatalf("expected the last message to be the reset one, got %q", resetMsg.Subject)
	}
	if !strings.Contains(resetMsg.From, "@reset.") {
		t.Fatalf("reset mail should come from the reset domain, got %q", resetMsg.From)
	}

	// And the verification message from the same run carries the other domain.
	var verifyMsg mail.Message
	found := false
	for _, m := range env.mailer.Sent() {
		if strings.Contains(m.Subject, "Confirm") {
			verifyMsg, found = m, true
		}
	}
	if !found {
		t.Fatal("expected a verification message from the registration")
	}
	if !strings.Contains(verifyMsg.From, "@verify.") {
		t.Fatalf("verification mail should come from the verify domain, got %q", verifyMsg.From)
	}
	if verifyMsg.From == resetMsg.From {
		t.Fatal("the two flows used the same sender")
	}
}

func TestRegisterSucceedsEvenWhenTheLinkCannotBeSent(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())
	env.mailer.FailWith(context.DeadlineExceeded)

	// A mail problem must not cost somebody their account.
	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "unlucky@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201 despite the mail failure, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestVerifyEmailFlow(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})
	token := tokenFromLastVerificationMail(t, env.mailer)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{
		"token": token,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	// The response carries the confirmed user and a session token, so the page the link
	// leads to can show the confirmed state and save details without asking for a password.
	var out struct {
		User  map[string]any `json:"user"`
		Token string         `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.User["emailVerifiedAt"] == nil {
		t.Fatalf("expected emailVerifiedAt to be set, got %v", out.User["emailVerifiedAt"])
	}
	if out.Token == "" {
		t.Fatal("expected a session token so the link signs the person in")
	}

	// The issued token must actually work, which is the point of returning one.
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", out.Token, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("the issued token was rejected: %d %s", me.Code, me.Body.String())
	}
	var profile map[string]any
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["emailVerifiedAt"] == nil {
		t.Fatal("the stored user is not verified")
	}
}

func TestVerifyEmailRejectsBadTokens(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	for _, token := range []string{"", "nope", strings.Repeat("a", 64)} {
		rec := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{
			"token": token,
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("token %q: expected 400, got %d %s", token, rec.Code, rec.Body.String())
		}
	}
}

func TestVerifyEmailIsSingleUseOverHTTP(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})
	token := tokenFromLastVerificationMail(t, env.mailer)

	first := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{"token": token})
	if first.Code != http.StatusOK {
		t.Fatalf("first use: %d", first.Code)
	}
	second := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{"token": token})
	if second.Code != http.StatusBadRequest {
		t.Fatalf("expected the token to be single-use, got %d", second.Code)
	}
}

func TestVerifyEmailWorksWithoutASession(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})
	token := tokenFromLastVerificationMail(t, env.mailer)

	// No Authorization header at all: the link is opened from a mail client.
	rec := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{"token": token})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 without a session, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestResendVerificationSendsANewLink(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})
	first := tokenFromLastVerificationMail(t, env.mailer)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/resend-verification", "", map[string]string{
		"email": "new@example.com",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resend: expected 204, got %d %s", rec.Code, rec.Body.String())
	}

	second := tokenFromLastVerificationMail(t, env.mailer)
	if first == second {
		t.Fatal("the resend reused the original token")
	}
}

func TestResendVerificationKeepsUnknownAddressesQuiet(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	// This endpoint is reachable signed out, so it must not become a way to discover who
	// has an account.
	rec := doJSON(t, env.handler, http.MethodPost, "/auth/resend-verification", "", map[string]string{
		"email": "nobody@example.com",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for an unknown address, got %d %s", rec.Code, rec.Body.String())
	}
	if len(env.mailer.Sent()) != 0 {
		t.Fatal("a message was sent for an unknown address")
	}
}

func TestResendVerificationReportsAnAlreadyVerifiedAddress(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})
	token := tokenFromLastVerificationMail(t, env.mailer)
	doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{"token": token})

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/resend-verification", "", map[string]string{
		"email": "new@example.com",
	})
	// Worth reporting: the caller owns the address, so it saves them waiting for a link
	// that is not coming.
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestResendVerificationIsRateLimited(t *testing.T) {
	env := newVerifyHTTPEnv(t, verifyLimits())
	from := "203.0.113.60:1111"

	// Register from a *different* address so its own attempts do not consume the caller
	// allowance this test is measuring.
	doJSONFrom(t, env.handler, "203.0.113.61:2222", http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new@example.com", "password": "password123",
	})

	for i := 1; i <= 3; i++ {
		rec := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/resend-verification", "", map[string]string{
			"email": "new@example.com",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("attempt %d: expected 204, got %d", i, rec.Code)
		}
	}
	assertRateLimited(t, doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/resend-verification", "", map[string]string{
		"email": "new@example.com",
	}))
}

func TestVerificationEndpointsDenyWhenNotConfigured(t *testing.T) {
	// newTestEnv has no EmailVerification, which mirrors a deployment without the feature.
	env := newTestEnv(t)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{
		"token": "anything",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}

	rec = doJSON(t, env.handler, http.MethodPost, "/auth/resend-verification", "", map[string]string{
		"email": "someone@example.com",
	})
	// Same answer as an unknown address, so it cannot be used to probe the configuration.
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}
