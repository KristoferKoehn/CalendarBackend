package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/mail"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

// newSignupEnv wires everything registration touches: the user service, so a sign-up profile
// can be applied, and verification, so the confirmation mail is exercised too.
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

func TestRegisterAppliesTheSuppliedProfile(t *testing.T) {
	env := newSignupEnv(t)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]any{
		"email":       "newcomer@example.com",
		"password":    "password123",
		"displayName": "New Comer",
		"pronouns":    "they/them",
		"jobTitle":    "Engineer",
		"bio":         "Says little.",
		"birthMonth":  6,
		"birthDay":    15,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	profile := signupProfile(t, env, "newcomer@example.com", "password123")
	for key, want := range map[string]any{
		"displayName": "New Comer",
		"pronouns":    "they/them",
		"jobTitle":    "Engineer",
		"bio":         "Says little.",
	} {
		if profile[key] != want {
			t.Errorf("%s: expected %v, got %v", key, want, profile[key])
		}
	}
	if profile["birthMonth"] != float64(6) || profile["birthDay"] != float64(15) {
		t.Errorf("birthday: expected 6/15, got %v/%v", profile["birthMonth"], profile["birthDay"])
	}

	// And the confirmation mail still went out, so the profile did not displace it.
	if len(env.mailer.Sent()) != 1 {
		t.Errorf("expected 1 verification message, got %d", len(env.mailer.Sent()))
	}
}

func TestRegisterWithoutAProfileStillWorks(t *testing.T) {
	env := newSignupEnv(t)

	// The profile is optional: requiring it would turn signing up into a form to be
	// defeated, and Settings exists for anyone who skips it.
	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "minimal@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	profile := signupProfile(t, env, "minimal@example.com", "password123")
	for _, key := range []string{"displayName", "pronouns", "jobTitle", "bio", "birthMonth", "birthDay"} {
		if profile[key] != nil {
			t.Errorf("%s: expected null, got %v", key, profile[key])
		}
	}
}

func TestRegisterRejectsABadBirthdayWithoutCreatingTheAccount(t *testing.T) {
	env := newSignupEnv(t)

	// The check runs before the account exists, so a rejection leaves nothing behind. The
	// alternative is somebody holding an account they were told they had not created.
	cases := []map[string]any{
		{"email": "bad1@example.com", "password": "password123", "birthMonth": 13, "birthDay": 1},
		{"email": "bad2@example.com", "password": "password123", "birthMonth": 6, "birthDay": 32},
		{"email": "bad3@example.com", "password": "password123", "birthMonth": 6},
		{"email": "bad4@example.com", "password": "password123", "birthDay": 15},
	}
	for _, body := range cases {
		rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", body)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%v: expected 400, got %d %s", body, rec.Code, rec.Body.String())
		}
	}

	// And none of them got an account: signing in must fail.
	for _, email := range []string{"bad1@example.com", "bad2@example.com", "bad3@example.com", "bad4@example.com"} {
		rec := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
			"email": email, "password": "password123",
		})
		if rec.Code == http.StatusOK {
			t.Errorf("%s has an account despite the rejected registration", email)
		}
	}
}

func TestRegisterIgnoresAnUnknownFieldRatherThanFailing(t *testing.T) {
	env := newSignupEnv(t)

	// A client sending something unrecognised should still get an account: the profile is a
	// convenience, not a gate.
	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]any{
		"email": "extra@example.com", "password": "password123", "favouriteColour": "green",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", rec.Code, rec.Body.String())
	}
}

// A profile problem must never be what stops somebody from having an account they were
// promised. The background send is the other half of the same idea.
func TestRegisterSurvivesTheMailerBeingUnavailable(t *testing.T) {
	env := newSignupEnv(t)
	env.mailer.FailWith(context.DeadlineExceeded)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]any{
		"email": "unlucky@example.com", "password": "password123", "displayName": "Unlucky",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", rec.Code, rec.Body.String())
	}
}
