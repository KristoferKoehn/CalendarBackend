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
		"resourceId": roomID, "date": "2026-10-10", "startTime": "14:00", "endTime": "15:00",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId": roomID, "date": "2026-10-10", "startTime": "14:30", "endTime": "15:30",
	})
	if rec.Code != http.StatusConflict {
		t.Fatalf("overlap %d: %s", rec.Code, rec.Body.String())
	}

	rec = doJSON(t, h, http.MethodPost, "/appointments", userToken, map[string]string{
		"resourceId": roomID, "date": "2026-03-08", "startTime": "02:30", "endTime": "03:30",
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
