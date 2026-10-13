package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

type resetHTTPEnv struct {
	handler  http.Handler
	users    *memory.UserStore
	sessions *memory.SessionStore
	mailer   *mail.Stub
}

// newResetHTTPEnv builds a handler with password reset wired up, which newTestEnv
// deliberately leaves out so the other tests are unaffected.
func newResetHTTPEnv(t *testing.T, limits config.RateLimit) *resetHTTPEnv {
	t.Helper()

	users := memory.NewUserStore()
	sessions := memory.NewSessionStore()
	tokens := memory.NewAuthTokenStore()
	resources := memory.NewResourceStore()
	appointments := memory.NewAppointmentStore()
	mailer := mail.NewStub()

	limiter := ratelimit.New(15 * time.Minute)
	t.Cleanup(limiter.Close)

	authSvc := core.NewAuthService(users, sessions, time.Hour, nil)
	resetSvc := core.NewPasswordReset(users, tokens, sessions, mailer, "https://example.test",
		core.DefaultResetTTL, nil)

	handler := NewHandler(Deps{
		Auth:          authSvc,
		Resources:     core.NewResourceService(resources),
		Users:         core.NewUserService(users, appointments, sessions),
		Appointments:  core.NewAppointmentService(appointments, resources, nil),
		Sessions:      sessions,
		UserStore:     users,
		Limiter:       limiter,
		RateLimit:     limits,
		Mailer:        mailer,
		PublicSiteURL: "https://example.test",
		PasswordReset: resetSvc,
	})

	return &resetHTTPEnv{handler: handler, users: users, sessions: sessions, mailer: mailer}
}

func resetLimits() config.RateLimit {
	return config.RateLimit{
		Login:          config.Limit{Failures: 5, Window: 15 * time.Minute},
		Register:       config.Limit{Failures: 50, Window: 15 * time.Minute},
		ForgotPassword: config.Limit{Failures: 3, Window: 15 * time.Minute},
		ResetPassword:  config.Limit{Failures: 10, Window: 15 * time.Minute},
	}
}

func (e *resetHTTPEnv) seedUser(t *testing.T, email, password string) {
	t.Helper()
	rec := doJSON(t, e.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": email, "password": password,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed register: %d %s", rec.Code, rec.Body.String())
	}
}

// tokenFromLastMail pulls the raw token out of the link in the recorded message.
func tokenFromLastMail(t *testing.T, mailer *mail.Stub) string {
	t.Helper()
	msg, ok := mailer.Last()
	if !ok {
		t.Fatal("no message was recorded")
	}
	const marker = "/reset-password?token="
	i := strings.Index(msg.Text, marker)
	if i < 0 {
		t.Fatalf("no reset link in:\n%s", msg.Text)
	}
	rest := msg.Text[i+len(marker):]
	if end := strings.IndexAny(rest, "\r\n "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

func TestForgotPasswordAnswersTheSameForKnownAndUnknownAddresses(t *testing.T) {
	env := newResetHTTPEnv(t, resetLimits())
	env.seedUser(t, "known@example.com", "password123")

	from := "203.0.113.40:1111"
	known := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "known@example.com"})
	unknown := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "nobody@example.com"})

	// The whole point: the two responses must be indistinguishable, or the endpoint
	// becomes a way to discover who has an account.
	if known.Code != unknown.Code {
		t.Fatalf("status differs: known=%d unknown=%d", known.Code, unknown.Code)
	}
	if known.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d %s", known.Code, known.Body.String())
	}
	if known.Body.String() != unknown.Body.String() {
		t.Fatalf("body differs: known=%q unknown=%q", known.Body.String(), unknown.Body.String())
	}

	// And mail only went to the real account.
	sent := env.mailer.Sent()
	if len(sent) != 1 {
		t.Fatalf("expected exactly 1 message, got %d", len(sent))
	}
	if sent[0].To != "known@example.com" {
		t.Fatalf("mail went to %q", sent[0].To)
	}
}

func TestForgotPasswordRejectsAMalformedAddress(t *testing.T) {
	env := newResetHTTPEnv(t, resetLimits())
	rec := doJSONFrom(t, env.handler, "203.0.113.41:1111", http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "not-an-email"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestForgotPasswordIsRateLimited(t *testing.T) {
	env := newResetHTTPEnv(t, resetLimits())
	env.seedUser(t, "known@example.com", "password123")

	from := "203.0.113.42:1111"
	for i := 1; i <= 3; i++ {
		rec := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/forgot-password", "",
			map[string]string{"email": "known@example.com"})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: expected 204, got %d", i, rec.Code)
		}
	}

	// Sending mail is the expensive, abusable action, so a success must cost.
	assertRateLimited(t, doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "known@example.com"}))
}

func TestResetPasswordSetsTheNewPasswordAndEndsSessions(t *testing.T) {
	env := newResetHTTPEnv(t, resetLimits())
	ctx := context.Background()
	env.seedUser(t, "known@example.com", "original-password")

	// Sign in first, so there is a live session to invalidate.
	login := doJSONFrom(t, env.handler, "203.0.113.43:1111", http.MethodPost, "/auth/login", "",
		map[string]string{"email": "known@example.com", "password": "original-password"})
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	oldToken := decodeStringField(t, login, "token")

	req := doJSONFrom(t, env.handler, "203.0.113.43:1111", http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "known@example.com"})
	if req.Code != http.StatusNoContent {
		t.Fatalf("forgot-password: %d", req.Code)
	}
	token := tokenFromLastMail(t, env.mailer)

	reset := doJSONFrom(t, env.handler, "203.0.113.43:1111", http.MethodPost, "/auth/reset-password", "",
		map[string]string{"token": token, "newPassword": "brand-new-password"})
	if reset.Code != http.StatusNoContent {
		t.Fatalf("reset: expected 204, got %d %s", reset.Code, reset.Body.String())
	}

	// The new password works.
	after := doJSONFrom(t, env.handler, "203.0.113.43:1111", http.MethodPost, "/auth/login", "",
		map[string]string{"email": "known@example.com", "password": "brand-new-password"})
	if after.Code != http.StatusOK {
		t.Fatalf("the new password was rejected: %d %s", after.Code, after.Body.String())
	}

	// The old one does not.
	stale := doJSONFrom(t, env.handler, "203.0.113.43:1111", http.MethodPost, "/auth/login", "",
		map[string]string{"email": "known@example.com", "password": "original-password"})
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("the old password still works: %d", stale.Code)
	}

	// The session that existed before the reset is dead: a reset that leaves the
	// previous holder signed in has not recovered the account.
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", oldToken, nil)
	if me.Code != http.StatusUnauthorized {
		t.Fatalf("the pre-reset session survived: %d %s", me.Code, me.Body.String())
	}
	_ = ctx
}

func TestResetPasswordRejectsBadTokensAndWeakPasswords(t *testing.T) {
	env := newResetHTTPEnv(t, resetLimits())
	from := "203.0.113.44:1111"

	cases := []struct {
		name     string
		body     map[string]string
		wantCode int
	}{
		{name: "unknown token", body: map[string]string{"token": "nope", "newPassword": "long-enough-password"}, wantCode: http.StatusBadRequest},
		{name: "empty token", body: map[string]string{"token": "", "newPassword": "long-enough-password"}, wantCode: http.StatusBadRequest},
		{name: "short password", body: map[string]string{"token": "nope", "newPassword": "short"}, wantCode: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/reset-password", "", tc.body)
			if rec.Code != tc.wantCode {
				t.Fatalf("expected %d, got %d %s", tc.wantCode, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestResetEndpointsDenyWhenNoResetServiceIsConfigured(t *testing.T) {
	// newTestEnv has no PasswordReset, which mirrors a deployment that has not
	// configured the feature. Neither route may become an account oracle.
	env := newTestEnv(t)
	from := "203.0.113.45:1111"

	reg := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/register", "",
		map[string]string{"email": "someone@example.com", "password": "password123"})
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d", reg.Code)
	}

	known := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "someone@example.com"})
	unknown := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "nobody@example.com"})

	if known.Code != unknown.Code || known.Code != http.StatusNoContent {
		t.Fatalf("expected identical 204s, got known=%d unknown=%d", known.Code, unknown.Code)
	}

	reset := doJSONFrom(t, env.handler, from, http.MethodPost, "/auth/reset-password", "",
		map[string]string{"token": "anything", "newPassword": "long-enough-password"})
	if reset.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", reset.Code)
	}
}

// decodeStringField pulls a top-level string field out of a JSON response body.
func decodeStringField(t *testing.T, rec *httptest.ResponseRecorder, field string) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("could not decode the response body %q: %v", rec.Body.String(), err)
	}
	v, ok := body[field].(string)
	if !ok {
		t.Fatalf("field %q missing or not a string in %q", field, rec.Body.String())
	}
	return v
}
