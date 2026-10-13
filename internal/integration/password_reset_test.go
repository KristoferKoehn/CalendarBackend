//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

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

// resetTokenFromMail pulls the raw token out of the link in the recorded message.
func resetTokenFromMail(t *testing.T) string {
	t.Helper()
	msg, ok := testMailer.Last()
	if !ok {
		t.Fatal("no message was recorded")
	}
	const marker = "/reset-password?token="
	i := strings.Index(msg.Text, marker)
	if i < 0 {
		t.Fatalf("no reset link in the message body:\n%s", msg.Text)
	}
	rest := msg.Text[i+len(marker):]
	if end := strings.IndexAny(rest, "\r\n "); end >= 0 {
		rest = rest[:end]
	}
	return rest
}

// TestPasswordResetFlowHTTP walks the whole reset journey against the real database,
// which is where the single-statement token consumption and the session deletion are
// actually proven.
func TestPasswordResetFlowHTTP(t *testing.T) {
	truncateAll(t)
	handler := newServer(t)

	const email = "reset-flow@example.com"
	const original = "original-password"

	reg := doJSON(t, handler, http.MethodPost, "/auth/register", "",
		map[string]string{"email": email, "password": original})
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", reg.Code, reg.Body.String())
	}

	// Sign in twice, to represent two devices with live sessions.
	first := doJSON(t, handler, http.MethodPost, "/auth/login", "",
		map[string]string{"email": email, "password": original})
	if first.Code != http.StatusOK {
		t.Fatalf("first login: %d", first.Code)
	}
	second := doJSON(t, handler, http.MethodPost, "/auth/login", "",
		map[string]string{"email": email, "password": original})
	if second.Code != http.StatusOK {
		t.Fatalf("second login: %d", second.Code)
	}
	tokenA := decodeStringField(t, first, "token")
	tokenB := decodeStringField(t, second, "token")

	// Ask for a reset. The response must not reveal whether the account exists.
	forgot := doJSON(t, handler, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": email})
	if forgot.Code != http.StatusNoContent {
		t.Fatalf("forgot-password: %d %s", forgot.Code, forgot.Body.String())
	}

	unknown := doJSON(t, handler, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": "no-such-account@example.com"})
	if unknown.Code != forgot.Code || unknown.Body.String() != forgot.Body.String() {
		t.Fatalf("known and unknown addresses differ: known=%d/%q unknown=%d/%q",
			forgot.Code, forgot.Body.String(), unknown.Code, unknown.Body.String())
	}

	token := resetTokenFromMail(t)
	if token == "" {
		t.Fatal("the reset link carried no token")
	}

	// A token is stored only as a hash. This is the property that makes a database
	// leak useless, so assert it rather than trusting it.
	var storedHash string
	if err := testPool.QueryRow(context.Background(),
		`SELECT token_hash FROM auth_tokens WHERE purpose = 'password_reset'`).Scan(&storedHash); err != nil {
		t.Fatalf("reading the stored token: %v", err)
	}
	if storedHash == token {
		t.Fatal("the raw token was stored verbatim")
	}
	if len(storedHash) != 64 {
		t.Fatalf("expected a SHA-256 hex digest, got %d characters", len(storedHash))
	}

	// Redeem it.
	reset := doJSON(t, handler, http.MethodPost, "/auth/reset-password", "",
		map[string]string{"token": token, "newPassword": "brand-new-password"})
	if reset.Code != http.StatusNoContent {
		t.Fatalf("reset: expected 204, got %d %s", reset.Code, reset.Body.String())
	}

	// The new password works and the old one does not.
	after := doJSON(t, handler, http.MethodPost, "/auth/login", "",
		map[string]string{"email": email, "password": "brand-new-password"})
	if after.Code != http.StatusOK {
		t.Fatalf("the new password was rejected: %d %s", after.Code, after.Body.String())
	}
	stale := doJSON(t, handler, http.MethodPost, "/auth/login", "",
		map[string]string{"email": email, "password": original})
	if stale.Code != http.StatusUnauthorized {
		t.Fatalf("the old password still works: %d", stale.Code)
	}

	// Both pre-reset sessions are gone, which is the point of a password reset.
	for name, tok := range map[string]string{"first device": tokenA, "second device": tokenB} {
		me := doJSON(t, handler, http.MethodGet, "/auth/me", tok, nil)
		if me.Code != http.StatusUnauthorized {
			t.Fatalf("the session on the %s survived the reset: %d", name, me.Code)
		}
	}

	// The link is single-use.
	replay := doJSON(t, handler, http.MethodPost, "/auth/reset-password", "",
		map[string]string{"token": token, "newPassword": "another-password"})
	if replay.Code != http.StatusBadRequest {
		t.Fatalf("expected the token to be single-use, got %d %s", replay.Code, replay.Body.String())
	}

	// And the rejected replay changed nothing.
	final := doJSON(t, handler, http.MethodPost, "/auth/login", "",
		map[string]string{"email": email, "password": "brand-new-password"})
	if final.Code != http.StatusOK {
		t.Fatalf("the replay altered the password: %d", final.Code)
	}
}

// TestIssuingANewResetLinkInvalidatesTheOldOne covers the case where somebody asks
// twice: only the newest link may work.
func TestIssuingANewResetLinkInvalidatesTheOldOne(t *testing.T) {
	truncateAll(t)
	handler := newServer(t)

	const email = "reset-twice@example.com"
	reg := doJSON(t, handler, http.MethodPost, "/auth/register", "",
		map[string]string{"email": email, "password": "original-password"})
	if reg.Code != http.StatusCreated {
		t.Fatalf("register: %d", reg.Code)
	}

	if rec := doJSON(t, handler, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": email}); rec.Code != http.StatusNoContent {
		t.Fatalf("first request: %d", rec.Code)
	}
	firstToken := resetTokenFromMail(t)

	if rec := doJSON(t, handler, http.MethodPost, "/auth/forgot-password", "",
		map[string]string{"email": email}); rec.Code != http.StatusNoContent {
		t.Fatalf("second request: %d", rec.Code)
	}
	secondToken := resetTokenFromMail(t)

	if firstToken == secondToken {
		t.Fatal("the second request reused the first token")
	}

	// Only one live row should remain for this user.
	var live int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM auth_tokens WHERE purpose = 'password_reset' AND consumed_at IS NULL`).Scan(&live); err != nil {
		t.Fatal(err)
	}
	if live != 1 {
		t.Fatalf("expected exactly 1 live reset token, found %d", live)
	}

	old := doJSON(t, handler, http.MethodPost, "/auth/reset-password", "",
		map[string]string{"token": firstToken, "newPassword": "from-old-link"})
	if old.Code != http.StatusBadRequest {
		t.Fatalf("the superseded link still works: %d", old.Code)
	}

	fresh := doJSON(t, handler, http.MethodPost, "/auth/reset-password", "",
		map[string]string{"token": secondToken, "newPassword": "from-new-link"})
	if fresh.Code != http.StatusNoContent {
		t.Fatalf("the newest link failed: %d %s", fresh.Code, fresh.Body.String())
	}
}
