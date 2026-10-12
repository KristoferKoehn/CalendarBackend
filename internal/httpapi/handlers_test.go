package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"kristoferkoehn/CalendarBackend/internal/auth"
	"kristoferkoehn/CalendarBackend/internal/core"
	"kristoferkoehn/CalendarBackend/internal/domain"
	"kristoferkoehn/CalendarBackend/internal/store/memory"
)

type testEnv struct {
	handler      http.Handler
	users        *memory.UserStore
	resources    *memory.ResourceStore
	appointments *memory.AppointmentStore
}

func newTestEnv(t *testing.T) *testEnv {
	t.Helper()
	users := memory.NewUserStore()
	sessions := memory.NewSessionStore()
	resources := memory.NewResourceStore()
	appointments := memory.NewAppointmentStore()

	handler := NewHandler(Deps{
		Auth:         core.NewAuthService(users, sessions, time.Hour, nil),
		Resources:    core.NewResourceService(resources),
		Users:        core.NewUserService(users, appointments, sessions),
		Appointments: core.NewAppointmentService(appointments, resources, nil),
		Sessions:     sessions,
		UserStore:    users,
		CORSOrigins:  []string{"http://localhost:3000"},
	})
	return &testEnv{handler: handler, users: users, resources: resources, appointments: appointments}
}

func doJSON(t *testing.T, h http.Handler, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
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

func TestRegisterAndLoginFlow(t *testing.T) {
	h := newTestEnv(t).handler

	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "a@example.com", "password": "password123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "a@example.com", "password": "password123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body %s", rec.Code, rec.Body.String())
	}
	var login loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	if login.Token == "" {
		t.Fatal("expected token")
	}

	rec = doJSON(t, h, http.MethodGet, "/auth/me", login.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me status = %d, body %s", rec.Code, rec.Body.String())
	}
	var user domain.User
	if err := json.Unmarshal(rec.Body.Bytes(), &user); err != nil {
		t.Fatal(err)
	}
	if user.Email != "a@example.com" {
		t.Fatalf("unexpected user: %+v", user)
	}
}

func TestUnauthenticated(t *testing.T) {
	h := newTestEnv(t).handler
	rec := doJSON(t, h, http.MethodGet, "/auth/me", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	assertErrorCode(t, rec, "UNAUTHENTICATED")
}

func TestInvalidInput(t *testing.T) {
	h := newTestEnv(t).handler
	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "not-an-email", "password": "password123"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	assertErrorCode(t, rec, "INVALID_INPUT")
}

func TestAdminOnlyForbidden(t *testing.T) {
	h := newTestEnv(t).handler

	doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "a@example.com", "password": "password123"})
	rec := doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "a@example.com", "password": "password123"})
	var login loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, h, http.MethodGet, "/users", login.Token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestAppointmentFlow(t *testing.T) {
	env := newTestEnv(t)
	h := env.handler

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.User{ID: uuid.New(), Email: "admin@example.com", Role: domain.RoleAdmin, CreatedAt: time.Now().UTC()}
	if _, err := env.users.CreateUser(context.Background(), admin, hash); err != nil {
		t.Fatal(err)
	}
	room := domain.Resource{ID: uuid.New(), Name: "Room A", Timezone: "America/Los_Angeles", Active: true, CreatedAt: time.Now().UTC()}
	if _, err := env.resources.CreateResource(context.Background(), room); err != nil {
		t.Fatal(err)
	}

	doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "u@example.com", "password": "password123"})
	rec := doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "u@example.com", "password": "password123"})
	var login loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", login.Token, map[string]string{
		"resourceId":  room.ID.String(),
		"title":       "Standup",
		"description": "Daily sync",
		"date":        "2026-10-10",
		"startTime":   "14:00",
		"endTime":     "15:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", rec.Code, rec.Body.String())
	}
	var created domain.Appointment
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Title != "Standup" || created.Description != "Daily sync" {
		t.Fatalf("title/description not echoed: %+v", created)
	}

	rec = doJSON(t, h, http.MethodGet, "/appointments", login.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d", rec.Code)
	}
	var list appointmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Appointments) != 1 {
		t.Fatalf("expected 1 appointment, got %d", len(list.Appointments))
	}
}

func assertErrorCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Error.Code != code {
		t.Fatalf("error code = %s, want %s", env.Error.Code, code)
	}
}

func TestDeleteUserFlow(t *testing.T) {
	env := newTestEnv(t)
	h := env.handler

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	admin := domain.User{ID: uuid.New(), Email: "admin@example.com", Role: domain.RoleAdmin, CreatedAt: time.Now().UTC()}
	if _, err := env.users.CreateUser(context.Background(), admin, hash); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "victim@example.com", "password": "password123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, body %s", rec.Code, rec.Body.String())
	}
	var reg registerResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	if reg.UserID == "" {
		t.Fatal("expected userId")
	}

	rec = doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "admin@example.com", "password": "password123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("admin login status = %d, body %s", rec.Code, rec.Body.String())
	}
	var adminLogin loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &adminLogin); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "victim@example.com", "password": "password123"})
	if rec.Code != http.StatusOK {
		t.Fatalf("victim login status = %d, body %s", rec.Code, rec.Body.String())
	}
	var victimLogin loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &victimLogin); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, h, http.MethodDelete, "/users/"+reg.UserID, victimLogin.Token, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete status = %d, body %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "FORBIDDEN")

	rec = doJSON(t, h, http.MethodDelete, "/users/"+reg.UserID, adminLogin.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete status = %d, body %s", rec.Code, rec.Body.String())
	}
	var deleted domain.User
	if err := json.Unmarshal(rec.Body.Bytes(), &deleted); err != nil {
		t.Fatal(err)
	}
	if deleted.Email != "victim@example.com" {
		t.Fatalf("deleted user = %+v", deleted)
	}
}

func TestAppointmentCreateRequiresTitle(t *testing.T) {
	h := newTestEnv(t).handler

	doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "u@example.com", "password": "password123"})
	rec := doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "u@example.com", "password": "password123"})
	var login loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", login.Token, map[string]string{
		"resourceId": uuid.New().String(),
		"date":       "2026-10-10",
		"startTime":  "14:00",
		"endTime":    "15:00",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d, body %s", rec.Code, rec.Body.String())
	}
	assertErrorCode(t, rec, "INVALID_INPUT")
}

func TestAppointmentListTitleFilter(t *testing.T) {
	env := newTestEnv(t)
	h := env.handler

	room := domain.Resource{ID: uuid.New(), Name: "Room A", Timezone: "America/Los_Angeles", Active: true, CreatedAt: time.Now().UTC()}
	if _, err := env.resources.CreateResource(context.Background(), room); err != nil {
		t.Fatal(err)
	}

	doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "u@example.com", "password": "password123"})
	rec := doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": "u@example.com", "password": "password123"})
	var login loginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}

	for _, spec := range []struct{ title, start, end string }{
		{"Standup", "14:00", "15:00"},
		{"Planning", "16:00", "17:00"},
	} {
		rec = doJSON(t, h, http.MethodPost, "/appointments", login.Token, map[string]string{
			"resourceId": room.ID.String(),
			"title":      spec.title,
			"date":       "2026-10-10",
			"startTime":  spec.start,
			"endTime":    spec.end,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s status = %d, body %s", spec.title, rec.Code, rec.Body.String())
		}
	}

	rec = doJSON(t, h, http.MethodGet, "/appointments?title=stan", login.Token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list status = %d, body %s", rec.Code, rec.Body.String())
	}
	var list appointmentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Appointments) != 1 || list.Appointments[0].Title != "Standup" {
		t.Fatalf("expected only Standup, got %+v", list.Appointments)
	}
}
