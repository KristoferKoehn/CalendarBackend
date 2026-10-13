package httpapi

import (
	"net/http"
	"testing"
	"time"

	"kristoferkoehn/CalendarBackend/internal/config"
)

// The bug this pins: registering counted every attempt against a small limit AND shared a
// bucket with resending confirmation links, so a handful of ordinary sign-ups and resends
// from one address blocked that address from registering at all. Real use looked like an
// attack because the same record call was used for both.
func TestOrdinarySignupsDoNotLockOutTheAddress(t *testing.T) {
	const from = "203.0.113.70:1111"
	// The shipped default, rather than a test-only number, so the test reflects what a
	// deployment actually allows.
	h := newRateLimitedEnv(t, config.RateLimit{
		Login:              config.Limit{Failures: 5, Window: 15 * time.Minute},
		Register:           config.Limit{Failures: 10, Window: 15 * time.Minute},
		ForgotPassword:     config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResetPassword:      config.Limit{Failures: 10, Window: 15 * time.Minute},
		VerifyEmail:        config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResendVerification: config.Limit{Failures: 20, Window: 15 * time.Minute},
	})

	// A team signing up from one office address, interleaved with confirmation resends.
	for i := 1; i <= 6; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
			"email": "teammate" + string(rune('0'+i)) + "@example.com", "password": "password123",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("signup %d should have been allowed, got %d %s", i, rec.Code, rec.Body.String())
		}

		resend := doJSONFrom(t, h, from, http.MethodPost, "/auth/resend-verification", "", map[string]string{
			"email": "teammate" + string(rune('0'+i)) + "@example.com",
		})
		if resend.Code != http.StatusNoContent {
			t.Fatalf("resend %d should have been allowed, got %d %s", i, resend.Code, resend.Body.String())
		}
	}

	// And the address still has room, which is the part that was broken.
	rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "teammate7@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("the seventh signup should still be allowed, got %d %s", rec.Code, rec.Body.String())
	}
}

// Registering and resending are separate scopes, so exhausting one must not spend the
// other. They shared a bucket before.
func TestRegisterAndResendHaveSeparateAllowances(t *testing.T) {
	const from = "203.0.113.71:1111"
	h := newRateLimitedEnv(t, config.RateLimit{
		Login:              config.Limit{Failures: 5, Window: 15 * time.Minute},
		Register:           config.Limit{Failures: 2, Window: 15 * time.Minute},
		ForgotPassword:     config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResetPassword:      config.Limit{Failures: 10, Window: 15 * time.Minute},
		VerifyEmail:        config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResendVerification: config.Limit{Failures: 3, Window: 15 * time.Minute},
	})

	// Spend the whole registration allowance.
	for i := 1; i <= 2; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
			"email": "a" + string(rune('0'+i)) + "@example.com", "password": "password123",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("signup %d: %d", i, rec.Code)
		}
	}
	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "a3@example.com", "password": "password123",
	}))

	// Resending is a different scope, so it still has its own room.
	rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/resend-verification", "", map[string]string{
		"email": "a1@example.com",
	})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("resend should be unaffected by the register limit, got %d %s", rec.Code, rec.Body.String())
	}
}

// A mistyped address is the ordinary case, and it must not use up the allowance in a couple
// of tries.
func TestForgotPasswordToleratesMistakes(t *testing.T) {
	const from = "203.0.113.72:1111"
	h := newRateLimitedEnv(t, config.RateLimit{
		Login:              config.Limit{Failures: 5, Window: 15 * time.Minute},
		Register:           config.Limit{Failures: 10, Window: 15 * time.Minute},
		ForgotPassword:     config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResetPassword:      config.Limit{Failures: 10, Window: 15 * time.Minute},
		VerifyEmail:        config.Limit{Failures: 10, Window: 15 * time.Minute},
		ResendVerification: config.Limit{Failures: 20, Window: 15 * time.Minute},
	})

	for i := 1; i <= 8; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/forgot-password", "", map[string]string{
			"email": "someone@example.com",
		})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d should have been allowed, got %d %s", i, rec.Code, rec.Body.String())
		}
	}
}

// Login is a failed-attempt limit and stays tight: a wrong password must still be cut off
// after a handful of tries.
func TestLoginStaysStrict(t *testing.T) {
	const from = "203.0.113.73:1111"
	h := newRateLimitedEnv(t, testLimits(5, 10))
	registerUser(t, h, from, "strict@example.com")

	for i := 1; i <= 5; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
			"email": "strict@example.com", "password": "wrong-password",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, rec.Code)
		}
	}
	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "strict@example.com", "password": "wrong-password",
	}))
}
