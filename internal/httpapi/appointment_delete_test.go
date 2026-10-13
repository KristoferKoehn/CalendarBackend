package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/domain"
)

// deleteTestEnv builds a handler with one admin, one ordinary user, a room, and an
// appointment owned by the ordinary user, plus the bearer tokens for both.
type deleteTestEnv struct {
	handler    http.Handler
	adminToken string
	userToken  string
	apptID     string
}

func newDeleteTestEnv(t *testing.T) deleteTestEnv {
	t.Helper()
	env := newTestEnv(t)
	h := env.handler
	ctx := context.Background()

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.User{
		ID: uuid.New(), Email: "admin@example.com",
		Role: domain.RoleAdmin, CreatedAt: time.Now().UTC(),
	}
	if _, err := env.users.CreateUser(ctx, admin, hash); err != nil {
		t.Fatal(err)
	}
	room := domain.Resource{
		ID: uuid.New(), Name: "Room A", Timezone: "America/Los_Angeles",
		Active: true, CreatedAt: time.Now().UTC(),
	}
	if _, err := env.resources.CreateResource(ctx, room); err != nil {
		t.Fatal(err)
	}

	// loginAs returns a bearer token, or fails the test.
	loginAs := func(email string) string {
		rec := doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{
			"email": email, "password": "password123",
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("login %s: %d %s", email, rec.Code, rec.Body.String())
		}
		var out loginResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Token
	}

	adminToken := loginAs("admin@example.com")

	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "booker@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	userToken := loginAs("booker@example.com")

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId":  room.ID.String(),
		"title":       "Standup",
		"description": "Daily sync",
		"date":        "2026-10-10",
		"startTime":   "14:00",
		"endTime":     "15:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create appointment: %d %s", rec.Code, rec.Body.String())
	}
	var created domain.Appointment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	return deleteTestEnv{
		handler:    h,
		adminToken: adminToken,
		userToken:  userToken,
		apptID:     created.ID.String(),
	}
}

// appointmentCount reports how many appointments the API lists for a given token.
func appointmentCount(t *testing.T, h http.Handler, token string) int {
	t.Helper()
	rec := doJSON(t, h, http.MethodGet, "/appointments", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	var list appointmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return len(list.Appointments)
}

// The subject of this test is the guard rather than the happy path: deleting erases
// the record, so it has to be admin-only. Each refusal is also checked for having left
// the row alone, so a passing 403 cannot be an artefact of the row already being gone.
func TestDeleteAppointmentIsAdminOnly(t *testing.T) {
	env := newDeleteTestEnv(t)

	if n := appointmentCount(t, env.handler, env.userToken); n != 1 {
		t.Fatalf("expected 1 appointment to begin with, got %d", n)
	}

	t.Run("anonymous is refused", func(t *testing.T) {
		rec := doJSON(t, env.handler, http.MethodDelete, "/appointments/"+env.apptID, "", nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "UNAUTHENTICATED")
		if n := appointmentCount(t, env.handler, env.adminToken); n != 1 {
			t.Fatalf("an unauthenticated delete removed the appointment: %d remain", n)
		}
	})

	t.Run("the owner is refused", func(t *testing.T) {
		// The owner is the strongest case for a non-admin: they may cancel and
		// reschedule this appointment, but erasing it is a different act.
		rec := doJSON(t, env.handler, http.MethodDelete, "/appointments/"+env.apptID, env.userToken, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
		}
		assertErrorCode(t, rec, "FORBIDDEN")
		if n := appointmentCount(t, env.handler, env.adminToken); n != 1 {
			t.Fatalf("the refused owner delete removed the appointment: %d remain", n)
		}
	})

	t.Run("an unrelated ordinary user is refused", func(t *testing.T) {
		reg := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
			"email": "bystander@example.com", "password": "password123",
		})
		if reg.Code != http.StatusCreated {
			t.Fatalf("register: %d %s", reg.Code, reg.Body.String())
		}
		login := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
			"email": "bystander@example.com", "password": "password123",
		})
		var out loginResponse
		if err := json.Unmarshal(login.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}

		rec := doJSON(t, env.handler, http.MethodDelete, "/appointments/"+env.apptID, out.Token, nil)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
		}
		if n := appointmentCount(t, env.handler, env.adminToken); n != 1 {
			t.Fatalf("a bystander delete removed the appointment: %d remain", n)
		}
	})

	t.Run("an admin succeeds, and only then is it gone", func(t *testing.T) {
		rec := doJSON(t, env.handler, http.MethodDelete, "/appointments/"+env.apptID, env.adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("admin delete: %d %s", rec.Code, rec.Body.String())
		}
		if n := appointmentCount(t, env.handler, env.userToken); n != 0 {
			t.Fatalf("expected the appointment to be gone, %d remain", n)
		}
	})
}

func TestDeleteAppointmentWithUnknownID(t *testing.T) {
	env := newDeleteTestEnv(t)

	// As an admin, an id that names nothing is a plain 404.
	rec := doJSON(t, env.handler, http.MethodDelete, "/appointments/"+uuid.New().String(), env.adminToken, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d %s", rec.Code, rec.Body.String())
	}

	// As an ordinary user the authorization check runs first, so this is a 403 rather
	// than a 404. The ordering is deliberate: a 404 here would confirm which ids exist
	// to someone with no right to ask.
	rec = doJSON(t, env.handler, http.MethodDelete, "/appointments/"+uuid.New().String(), env.userToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 before existence is considered, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteAppointmentRejectsMalformedID(t *testing.T) {
	env := newDeleteTestEnv(t)

	// The id is parsed before authorization, so this is a 400 for anyone. A malformed
	// id cannot name a real appointment, so it reveals nothing either way.
	rec := doJSON(t, env.handler, http.MethodDelete, "/appointments/not-a-uuid", env.adminToken, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d %s", rec.Code, rec.Body.String())
	}
}
