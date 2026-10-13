package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"kristoferkoehn/CalendarBackend/internal/config"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/ratelimit"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

// newRateLimitedEnv builds a handler with throttling enabled, unlike newTestEnv
// which leaves it off so the other tests are unaffected.
func newRateLimitedEnv(t *testing.T, limits config.RateLimit) http.Handler {
	t.Helper()
	users := memory.NewUserStore()
	sessions := memory.NewSessionStore()
	resources := memory.NewResourceStore()
	appointments := memory.NewAppointmentStore()

	limiter := ratelimit.New(15 * time.Minute)
	t.Cleanup(limiter.Close)

	return NewHandler(Deps{
		Auth:         core.NewAuthService(users, sessions, time.Hour, nil),
		Resources:    core.NewResourceService(resources),
		Users:        core.NewUserService(users, appointments, sessions),
		Appointments: core.NewAppointmentService(appointments, resources, nil),
		Sessions:     sessions,
		UserStore:    users,
		Limiter:      limiter,
		RateLimit:    limits,
	})
}

// doJSONFrom is doJSON with a specific caller address, so tests can control how
// requests are attributed for throttling.
func doJSONFrom(t *testing.T, h http.Handler, remoteAddr, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	// An empty address keeps whatever httptest assigned, so a test mixing explicit
	// and implicit callers still attributes them consistently.
	if remoteAddr != "" {
		req.RemoteAddr = remoteAddr
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func testLimits(loginFailures, registerFailures int) config.RateLimit {
	return config.RateLimit{
		Login:    config.Limit{Failures: loginFailures, Window: 15 * time.Minute},
		Register: config.Limit{Failures: registerFailures, Window: 15 * time.Minute},
	}
}

func assertRateLimited(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429, got %d, body %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "RATE_LIMITED")

	// A client cannot back off sensibly without this header.
	header := rec.Header().Get("Retry-After")
	if header == "" {
		t.Fatal("expected a Retry-After header")
	}
	seconds, err := strconv.Atoi(header)
	if err != nil || seconds < 1 {
		t.Fatalf("Retry-After should be a positive number of seconds, got %q", header)
	}
}

// registerUser creates an account as if from `from`, so it shares the caller
// allowance with the requests that follow.
func registerUser(t *testing.T, h http.Handler, from, email string) {
	t.Helper()
	rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
		"email": email, "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %s: %d %s", email, rec.Code, rec.Body.String())
	}
}

func TestLoginThrottledPerAccount(t *testing.T) {
	const from = "203.0.113.10:1111"
	h := newRateLimitedEnv(t, testLimits(3, 5))
	registerUser(t, h, from, "victim@example.com")

	// Three wrong passwords are tolerated.
	for i := 1; i <= 3; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
			"email": "victim@example.com", "password": "wrong-password",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: expected 401, got %d", i, rec.Code)
		}
	}

	// The fourth is refused, and it is the account that is blocked: a *correct*
	// password from a different address is still refused, which is the point of
	// scoping the limit to the account rather than only the caller.
	rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "victim@example.com", "password": "password123",
	})
	assertRateLimited(t, rec)

	rec = doJSONFrom(t, h, "198.51.100.4:2222", http.MethodPost, "/auth/login", "", map[string]string{
		"email": "victim@example.com", "password": "password123",
	})
	assertRateLimited(t, rec)
}

func TestLoginThrottledPerCaller(t *testing.T) {
	const from = "203.0.113.11:3333"
	h := newRateLimitedEnv(t, testLimits(3, 5))
	registerUser(t, h, from, "a@example.com")
	registerUser(t, h, from, "b@example.com")

	// Three failures from this address is the allowance.
	for _, email := range []string{"a@example.com", "b@example.com", "a@example.com"} {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
			"email": email, "password": "wrong-password",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", rec.Code)
		}
	}

	// A *correct* password still gets through. The caller limit exists to slow
	// guessing, and refusing a valid credential because someone else burned the
	// address's allowance would break every shared office address. This is the
	// behaviour the account-before/caller-after ordering exists to produce.
	rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "b@example.com", "password": "password123",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("a correct password should still sign in, got %d %s", rec.Code, rec.Body.String())
	}

	// Another wrong password is now refused, which is the actual protection.
	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "a@example.com", "password": "wrong-password",
	}))

	// A different caller is unaffected.
	rec = doJSONFrom(t, h, "198.51.100.5:4444", http.MethodPost, "/auth/login", "", map[string]string{
		"email": "b@example.com", "password": "password123",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("a different caller should not be throttled, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSuccessfulLoginClearsTheAccountCounter(t *testing.T) {
	const from = "203.0.113.12:5555"
	h := newRateLimitedEnv(t, testLimits(3, 5))
	registerUser(t, h, from, "careful@example.com")

	// Two typos, then the right password.
	for i := 0; i < 2; i++ {
		doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
			"email": "careful@example.com", "password": "wrong-password",
		})
	}
	rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "careful@example.com", "password": "password123",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", rec.Code, rec.Body.String())
	}

	// The success cleared the *account's* failures, so the account is no longer
	// what is stopping anyone. The caller has still used two of its three, so one
	// more wrong password is answered honestly...
	rec = doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "careful@example.com", "password": "wrong-password",
	})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", rec.Code, rec.Body.String())
	}

	// ...and the next is refused. If the successful sign-in had not cleared the
	// account counter, this would already have been refused two attempts earlier.
	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "careful@example.com", "password": "wrong-password",
	}))

	// The reset was scoped to the account: a *different* account from the same
	// address is still refused, because the caller's own allowance is spent.
	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "another@example.com", "password": "wrong-password",
	}))
}

func TestMalformedRequestsDoNotCountTowardsTheLimit(t *testing.T) {
	const from = "203.0.113.13:6666"
	h := newRateLimitedEnv(t, testLimits(2, 5))

	// A missing password is a bad request, not a failed sign-in, so it must not
	// consume the allowance.
	for i := 0; i < 10; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
			"email": "someone@example.com", "password": "",
		})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
		}
	}

	// The allowance is intact: two wrong passwords are still tolerated.
	for i := 1; i <= 2; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
			"email": "someone@example.com", "password": "wrong-password",
		})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d should have been a 401, got %d", i, rec.Code)
		}
	}
	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "someone@example.com", "password": "wrong-password",
	}))
}

func TestRegisterThrottledPerCaller(t *testing.T) {
	const from = "203.0.113.14:7777"
	h := newRateLimitedEnv(t, testLimits(5, 2))

	// Two accounts are allowed from one address.
	for i := 1; i <= 2; i++ {
		rec := doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
			"email": "new" + strconv.Itoa(i) + "@example.com", "password": "password123",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("registration %d: expected 201, got %d %s", i, rec.Code, rec.Body.String())
		}
	}

	assertRateLimited(t, doJSONFrom(t, h, from, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new3@example.com", "password": "password123",
	}))

	// A duplicate email is a failed attempt too, so it also counts and is refused
	// once the allowance is gone.
	rec := doJSONFrom(t, h, "198.51.100.6:8888", http.MethodPost, "/auth/register", "", map[string]string{
		"email": "new1@example.com", "password": "password123",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for a duplicate email, got %d %s", rec.Code, rec.Body.String())
	}
	rec = doJSONFrom(t, h, "198.51.100.6:8888", http.MethodPost, "/auth/register", "", map[string]string{
		"email": "another@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", rec.Code, rec.Body.String())
	}
	assertRateLimited(t, doJSONFrom(t, h, "198.51.100.6:8888", http.MethodPost, "/auth/register", "", map[string]string{
		"email": "third@example.com", "password": "password123",
	}))
}
