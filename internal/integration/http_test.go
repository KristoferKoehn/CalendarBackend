//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"kristoferkoehn/CalendarBackend/internal/seed"
)

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

func loginToken(t *testing.T, h http.Handler, email, password string) string {
	t.Helper()
	rec := doJSON(t, h, http.MethodPost, "/auth/login", "", map[string]string{"email": email, "password": password})
	if rec.Code != http.StatusOK {
		t.Fatalf("login status %d, body %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token
}

func TestAuthFlowHTTP(t *testing.T) {
	truncateAll(t)
	h := newServer(t)

	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "a@example.com", "password": "password123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %d: %s", rec.Code, rec.Body.String())
	}

	token := loginToken(t, h, "a@example.com", "password123")

	rec = doJSON(t, h, http.MethodGet, "/auth/me", token, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "a@example.com", "password": "password123"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate register %d", rec.Code)
	}
}

func TestAppointmentFlowHTTP(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)
	adminToken := loginToken(t, h, "admin@example.com", "password123")

	doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "u@example.com", "password": "password123"})
	userToken := loginToken(t, h, "u@example.com", "password123")

	rec := doJSON(t, h, http.MethodGet, "/users", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin users %d", rec.Code)
	}
	rec = doJSON(t, h, http.MethodGet, "/users", userToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin users %d", rec.Code)
	}

	rec = doJSON(t, h, http.MethodGet, "/resources", userToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources %d: %s", rec.Code, rec.Body.String())
	}
	var resources struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) == 0 {
		t.Fatal("expected seeded resources")
	}
	roomID := resources.Resources[0].ID

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId": roomID, "title": "Standup", "date": "2026-10-10", "startTime": "14:00", "endTime": "15:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId": roomID, "title": "Standup", "date": "2026-10-10", "startTime": "14:30", "endTime": "15:30",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("overlap %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId": roomID, "title": "Standup", "date": "2026-03-08", "startTime": "02:30", "endTime": "03:30",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("dst gap %d: %s", rec.Code, rec.Body.String())
	}
}

func TestMCPFlow(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)

	rec := doJSON(t, h, http.MethodPost, "/mcp", "", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"})
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/mcp", "", map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "auth.login", "arguments": map[string]any{"email": "admin@example.com", "password": "password123"}},
	})
	token := extractMCPToken(t, rec)

	rec = doJSON(t, h, http.MethodPost, "/mcp", token, map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]any{"name": "resources.list", "arguments": map[string]any{}},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("resources.list %d: %s", rec.Code, rec.Body.String())
	}
}

func extractMCPToken(t *testing.T, rec *httptest.ResponseRecorder) string {
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
		t.Fatal("no content")
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(resp.Result.Content[0].Text), &out); err != nil {
		t.Fatal(err)
	}
	return out.Token
}

func TestDeleteUserFlowHTTP(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)
	adminToken := loginToken(t, h, "admin@example.com", "password123")

	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "delete@example.com", "password": "password123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %d: %s", rec.Code, rec.Body.String())
	}
	var reg struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	userToken := loginToken(t, h, "delete@example.com", "password123")

	rec = doJSON(t, h, http.MethodDelete, "/users/"+reg.UserID, userToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-admin delete %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodDelete, "/users/"+reg.UserID, adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin delete %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodGet, "/users", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Users []struct {
			ID string `json:"id"`
		} `json:"users"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	for _, u := range list.Users {
		if u.ID == reg.UserID {
			t.Fatal("deleted user still listed")
		}
	}
}

func TestDeleteUserWithAppointmentHTTP(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)
	adminToken := loginToken(t, h, "admin@example.com", "password123")

	rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "busy@example.com", "password": "password123"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register %d: %s", rec.Code, rec.Body.String())
	}
	var reg struct {
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	userToken := loginToken(t, h, "busy@example.com", "password123")

	rec = doJSON(t, h, http.MethodGet, "/resources", userToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources %d: %s", rec.Code, rec.Body.String())
	}
	var resources struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) == 0 {
		t.Fatal("expected seeded resources")
	}
	roomID := resources.Resources[0].ID

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId": roomID, "title": "Busy", "date": "2026-10-10", "startTime": "14:00", "endTime": "15:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create appointment %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodDelete, "/users/"+reg.UserID, adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete with appointment %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDeleteLastAdminHTTP(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)
	adminToken := loginToken(t, h, "admin@example.com", "password123")

	rec := doJSON(t, h, http.MethodGet, "/auth/me", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("me %d: %s", rec.Code, rec.Body.String())
	}
	var me struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &me); err != nil {
		t.Fatal(err)
	}

	rec = doJSON(t, h, http.MethodDelete, "/users/"+me.ID, adminToken, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete last admin %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAppointmentTitleFilterHTTP(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)
	doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{"email": "filter@example.com", "password": "password123"})
	userToken := loginToken(t, h, "filter@example.com", "password123")

	rec := doJSON(t, h, http.MethodGet, "/resources", userToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources %d: %s", rec.Code, rec.Body.String())
	}
	var resources struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) == 0 {
		t.Fatal("expected seeded resources")
	}
	roomID := resources.Resources[0].ID

	for _, spec := range []struct{ title, start, end string }{
		{"Standup", "14:00", "15:00"},
		{"Planning", "16:00", "17:00"},
	} {
		rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
			"resourceId": roomID, "title": spec.title, "date": "2026-10-10", "startTime": spec.start, "endTime": spec.end,
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s %d: %s", spec.title, rec.Code, rec.Body.String())
		}
	}

	rec = doJSON(t, h, http.MethodGet, "/appointments?title=stan", userToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("filtered list %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Appointments []struct {
			Title string `json:"title"`
		} `json:"appointments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Appointments) != 1 || list.Appointments[0].Title != "Standup" {
		t.Fatalf("expected only Standup, got %+v", list.Appointments)
	}
}

// TestAppointmentVisibilityIsSharedHTTP proves that appointments are readable by
// every authenticated user, not just their owner. Write access stays restricted:
// only the owner or an admin may cancel or reschedule.
func TestAppointmentVisibilityIsSharedHTTP(t *testing.T) {
	truncateAll(t)
	ctx := context.Background()
	st := newStore()
	if err := seed.Run(ctx, st, st); err != nil {
		t.Fatal(err)
	}

	h := newServer(t)

	for _, email := range []string{"viewer-a@example.com", "viewer-b@example.com"} {
		rec := doJSON(t, h, http.MethodPost, "/auth/register", "", map[string]string{
			"email": email, "password": "password123",
		})
		if rec.Code != http.StatusCreated {
			t.Fatalf("register %s: %d %s", email, rec.Code, rec.Body.String())
		}
	}
	tokenA := loginToken(t, h, "viewer-a@example.com", "password123")
	tokenB := loginToken(t, h, "viewer-b@example.com", "password123")

	rec := doJSON(t, h, http.MethodGet, "/resources", tokenA, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("resources %d: %s", rec.Code, rec.Body.String())
	}
	var resources struct {
		Resources []struct {
			ID string `json:"id"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resources); err != nil {
		t.Fatal(err)
	}
	if len(resources.Resources) == 0 {
		t.Fatal("expected seeded resources")
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", tokenA, map[string]string{
		"resourceId": resources.Resources[0].ID,
		"title":      "Private planning",
		"date":       "2026-10-14",
		"startTime":  "09:00",
		"endTime":    "10:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		UserID string `json:"userId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}

	// User B can see user A's appointment.
	rec = doJSON(t, h, http.MethodGet, "/appointments", tokenB, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list %d: %s", rec.Code, rec.Body.String())
	}
	var all struct {
		Appointments []struct {
			ID     string `json:"id"`
			Title  string `json:"title"`
			UserID string `json:"userId"`
		} `json:"appointments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &all); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, a := range all.Appointments {
		if a.ID == created.ID {
			found = true
			if a.UserID != created.UserID {
				t.Fatalf("expected owner %s, got %s", created.UserID, a.UserID)
			}
		}
	}
	if !found {
		t.Fatalf("user B did not see user A's appointment: %+v", all.Appointments)
	}

	// A non-admin may filter by another user.
	rec = doJSON(t, h, http.MethodGet, "/appointments?userId="+created.UserID, tokenB, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("userId filter %d: %s", rec.Code, rec.Body.String())
	}
	var byUser struct {
		Appointments []struct {
			ID     string `json:"id"`
			UserID string `json:"userId"`
		} `json:"appointments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &byUser); err != nil {
		t.Fatal(err)
	}
	// Assert on membership rather than an exact count, so the test stays valid if
	// other fixtures ever seed appointments for this user.
	var matched bool
	for _, a := range byUser.Appointments {
		if a.ID != created.ID {
			continue
		}
		matched = true
		if a.UserID != created.UserID {
			t.Fatalf("filter returned wrong owner: want %s, got %s", created.UserID, a.UserID)
		}
	}
	if !matched {
		t.Fatalf("userId filter did not return the appointment: %+v", byUser.Appointments)
	}
	for _, a := range byUser.Appointments {
		if a.UserID != created.UserID {
			t.Fatalf("userId filter returned another owner's appointment: %+v", a)
		}
	}

	// But user B still may not write to it.
	rec = doJSON(t, h, http.MethodPost, "/appointments/"+created.ID+"/cancel", tokenB, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 cancelling another user's appointment, got %d: %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, h, http.MethodPost, "/appointments/"+created.ID+"/reschedule", tokenB, map[string]string{
		"title": "Hijacked", "date": "2026-10-14", "startTime": "11:00", "endTime": "12:00",
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 rescheduling another user's appointment, got %d: %s", rec.Code, rec.Body.String())
	}
}
