package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

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
		Appointments: core.NewAppointmentService(appointments, resources, nil),
		Sessions:     sessions,
		UserStore:    users,
	})
	return &testEnv{handler: handler, users: users, resources: resources, appointments: appointments}
}

func doRPC(t *testing.T, h http.Handler, method string, params any, token string) *httptest.ResponseRecorder {
	t.Helper()
	body := map[string]any{"jsonrpc": "2.0", "id": 1, "method": method}
	if params != nil {
		body["params"] = params
	}
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func callTool(t *testing.T, h http.Handler, name string, args any, token string) *httptest.ResponseRecorder {
	t.Helper()
	return doRPC(t, h, "tools/call", map[string]any{"name": name, "arguments": args}, token)
}

func TestInitialize(t *testing.T) {
	h := newTestEnv(t).handler
	rec := doRPC(t, h, "initialize", nil, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Result.ProtocolVersion == "" {
		t.Fatal("expected protocolVersion")
	}
}

func TestToolsList(t *testing.T) {
	h := newTestEnv(t).handler
	rec := doRPC(t, h, "tools/list", nil, "")
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Result.Tools) != 8 {
		t.Fatalf("expected 8 tools, got %d", len(resp.Result.Tools))
	}

	// Guard against a tool being added to the registry without a test or docs
	// update: assert the exact expected set.
	want := map[string]bool{
		"auth.register": true, "auth.login": true,
		"resources.list": true,
		"appointments.create": true, "appointments.list": true,
		"appointments.cancel": true, "appointments.reschedule": true,
		"appointments.delete": true,
	}
	got := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		got[tool.Name] = true
	}
	for name := range want {
		if !got[name] {
			t.Errorf("missing tool %q", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("unexpected tool %q", name)
		}
	}
}

func TestLoginAndProtectedTool(t *testing.T) {
	env := newTestEnv(t)
	h := env.handler

	rec := callTool(t, h, "auth.register", map[string]any{"email": "a@example.com", "password": "password123"}, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("register status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = callTool(t, h, "auth.login", map[string]any{"email": "a@example.com", "password": "password123"}, "")
	token := extractToken(t, rec)

	rec = callTool(t, h, "resources.list", map[string]any{}, "")
	assertRPCErrorCode(t, rec, "UNAUTHENTICATED")

	rec = callTool(t, h, "resources.list", map[string]any{}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources.list status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestAppointmentFlow(t *testing.T) {
	env := newTestEnv(t)
	h := env.handler

	room := domain.Resource{ID: uuid.New(), Name: "Room A", Timezone: "America/Los_Angeles", Active: true, CreatedAt: time.Now().UTC()}
	if _, err := env.resources.CreateResource(context.Background(), room); err != nil {
		t.Fatal(err)
	}

	callTool(t, h, "auth.register", map[string]any{"email": "a@example.com", "password": "password123"}, "")
	rec := callTool(t, h, "auth.login", map[string]any{"email": "a@example.com", "password": "password123"}, "")
	token := extractToken(t, rec)

	rec = callTool(t, h, "appointments.create", map[string]any{
		"resourceId":  room.ID.String(),
		"title":       "Standup",
		"description": "Daily sync",
		"date":        "2026-10-10",
		"startTime":   "14:00",
		"endTime":     "15:00",
	}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status %d, body %s", rec.Code, rec.Body.String())
	}

	rec = callTool(t, h, "appointments.list", map[string]any{"title": "stan"}, token)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status %d, body %s", rec.Code, rec.Body.String())
	}
	var listResp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Result.Content) == 0 {
		t.Fatal("no content in list result")
	}
	var out struct {
		Appointments []domain.Appointment `json:"appointments"`
	}
	if err := json.Unmarshal([]byte(listResp.Result.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Appointments) != 1 || out.Appointments[0].Title != "Standup" {
		t.Fatalf("expected only Standup, got %+v", out.Appointments)
	}
}

// Only admin accounts may delete an appointment, and a deleted appointment is
// gone for good.
func TestAppointmentDeleteRequiresAdmin(t *testing.T) {
	env := newTestEnv(t)
	h := env.handler
	ctx := context.Background()

	room := domain.Resource{ID: uuid.New(), Name: "Room A", Timezone: "America/Los_Angeles", Active: true, CreatedAt: time.Now().UTC()}
	if _, err := env.resources.CreateResource(ctx, room); err != nil {
		t.Fatal(err)
	}

	callTool(t, h, "auth.register", map[string]any{"email": "plain@example.com", "password": "password123"}, "")
	rec := callTool(t, h, "auth.login", map[string]any{"email": "plain@example.com", "password": "password123"}, "")
	plainToken := extractToken(t, rec)

	rec = callTool(t, h, "appointments.create", map[string]any{
		"resourceId": room.ID.String(),
		"title":      "Doomed",
		"date":       "2026-10-10",
		"startTime":  "09:00",
		"endTime":    "10:00",
	}, plainToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("create status %d, body %s", rec.Code, rec.Body.String())
	}

	var created struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if len(created.Result.Content) == 0 {
		t.Fatalf("no content in create result: %s", rec.Body.String())
	}
	var appt domain.Appointment
	if err := json.Unmarshal([]byte(created.Result.Content[0].Text), &appt); err != nil {
		t.Fatal(err)
	}
	apptID := appt.ID

	// A non-admin may not delete an appointment — not even their own. Users
	// cancel; only admins erase.
	rec = callTool(t, h, "appointments.delete", map[string]any{"appointmentId": apptID.String()}, plainToken)
	assertRPCErrorCode(t, rec, "FORBIDDEN")

	// Promote a second account to admin and delete with it.
	callTool(t, h, "auth.register", map[string]any{"email": "boss@example.com", "password": "password123"}, "")
	admin, _, err := env.users.GetUserByEmail(ctx, "boss@example.com")
	if err != nil {
		t.Fatal(err)
	}
	role := domain.RoleAdmin
	if _, err := env.users.UpdateUser(ctx, admin.ID, &role, nil); err != nil {
		t.Fatal(err)
	}
	rec = callTool(t, h, "auth.login", map[string]any{"email": "boss@example.com", "password": "password123"}, "")
	adminToken := extractToken(t, rec)

	rec = callTool(t, h, "appointments.delete", map[string]any{"appointmentId": apptID.String()}, adminToken)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete status %d, body %s", rec.Code, rec.Body.String())
	}

	// It is really gone.
	if _, err := env.appointments.GetAppointmentByID(ctx, apptID); err == nil {
		t.Fatal("expected the appointment to be deleted")
	}

	// Deleting it again is NOT_FOUND.
	rec = callTool(t, h, "appointments.delete", map[string]any{"appointmentId": apptID.String()}, adminToken)
	assertRPCErrorCode(t, rec, "NOT_FOUND")
}

func extractToken(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var resp struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Result.Content) == 0 {
		t.Fatal("no content in result")
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	if out.Token == "" {
		t.Fatal("expected token")
	}
	return out.Token
}

func assertRPCErrorCode(t *testing.T, rec *httptest.ResponseRecorder, code string) {
	t.Helper()
	var resp struct {
		Error *struct {
			Data struct {
				Code string `json:"code"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil || resp.Error.Data.Code != code {
		t.Fatalf("expected error code %s, body %s", code, rec.Body.String())
	}
}
