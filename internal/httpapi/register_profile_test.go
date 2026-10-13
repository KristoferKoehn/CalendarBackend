package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

// newSignupEnv wires everything the sign-up and verification flows touch.
func newSignupEnv(t *testing.T) *verifyHTTPEnv {
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
		RateLimit:         verifyLimits(),
		Mailer:            mailer,
		PublicSiteURL:     "https://example.test",
		EmailVerification: core.NewEmailVerification(users, tokens, mailer, "https://example.test", "CalendarSite <no-reply@verify.example.test>", core.DefaultVerificationTTL, nil),
	})

	return &verifyHTTPEnv{handler: handler, users: users, mailer: mailer}
}

// signupProfile reads a user back by signing in as them and asking for their own profile.
func signupProfile(t *testing.T, env *verifyHTTPEnv, email, password string) map[string]any {
	t.Helper()

	login := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
		"email": email, "password": password,
	})
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var out loginResponse
	if err := json.Unmarshal(login.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", out.Token, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("me: %d %s", me.Code, me.Body.String())
	}
	var profile map[string]any
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	return profile
}

// registerAdmin creates an admin directly in the store and returns a session token.
//
// There is no API for creating the first admin — a route that could would be a way in — so
// tests do what the seed does and write the row, then sign in normally.
func registerAdmin(t *testing.T, env *verifyHTTPEnv, email string) string {
	t.Helper()

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.User{
		ID:        uuid.New(),
		Email:     email,
		Role:      domain.RoleAdmin,
		CreatedAt: time.Now().UTC(),
	}
	if _, err := env.users.CreateUser(context.Background(), admin, hash); err != nil {
		t.Fatal(err)
	}

	login := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
		"email": email, "password": "password123",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("admin login: %d %s", login.Code, login.Body.String())
	}
	var out loginResponse
	if err := json.Unmarshal(login.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token
}
