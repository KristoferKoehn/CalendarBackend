package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// Creating an account is now only an address and a password. Details are collected on the
// screen the confirmation link leads to, so anything else sent here is ignored rather than
// applied — the request type no longer carries it.
func TestRegisterIgnoresAnythingBeyondCredentials(t *testing.T) {
	env := newSignupEnv(t)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]any{
		"email":       "extra@example.com",
		"password":    "password123",
		"displayName": "Should Be Ignored",
		"birthMonth":  6,
		"birthDay":    15,
		"bio":         "Also ignored.",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d %s", rec.Code, rec.Body.String())
	}

	// Sign in and confirm nothing beyond the credentials was stored. Signing in works even
	// unverified, which is what makes the prompt reachable.
	login := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "extra@example.com", "password": "password123",
	})
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	var out loginResponse
	if err := json.Unmarshal(login.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", out.Token, nil)
	var profile map[string]any
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["displayName"] != nil {
		t.Errorf("displayName should not have been taken from the registration, got %v",
			profile["displayName"])
	}
	if profile["birthMonth"] != nil || profile["birthDay"] != nil {
		t.Errorf("birthday should not have been taken from the registration, got %v/%v",
			profile["birthMonth"], profile["birthDay"])
	}
	if profile["emailVerifiedAt"] != nil {
		t.Errorf("a fresh account must not be verified, got %v", profile["emailVerifiedAt"])
	}
}

// A new account starts unverified, which is what the flow depends on: an account that began
// verified would skip straight past the details screen.
func TestRegisterLeavesTheAccountUnverified(t *testing.T) {
	env := newSignupEnv(t)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "fresh@example.com", "password": "password123",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}

	login := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "fresh@example.com", "password": "password123",
	})
	var out loginResponse
	if err := json.Unmarshal(login.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", out.Token, nil)
	var profile map[string]any
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["emailVerifiedAt"] != nil {
		t.Fatalf("expected unverified, got %v", profile["emailVerifiedAt"])
	}
}

// The confirmation link signs the person in, so the details screen behind it does not have
// to ask for a password moments after the address was proven by other means.
func TestVerifyEmailSignsTheUserIn(t *testing.T) {
	env := newSignupEnv(t)

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "link@example.com", "password": "password123",
	})
	token := tokenFromLastVerificationMail(t, env.mailer)

	rec := doJSON(t, env.handler, http.MethodPost, "/auth/verify-email", "", map[string]string{
		"token": token,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("verify: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		User  map[string]any `json:"user"`
		Token string         `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	// The token must be usable on its own, with no password having been sent.
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", out.Token, nil)
	if me.Code != http.StatusOK {
		t.Fatalf("the token from the link was rejected: %d %s", me.Code, me.Body.String())
	}
	var profile map[string]any
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile["email"] != "link@example.com" {
		t.Fatalf("the token belongs to the wrong account: %v", profile["email"])
	}
	if profile["emailVerifiedAt"] == nil {
		t.Fatal("expected the account to be verified")
	}
}

// An admin has to be able to let somebody in who cannot receive the mail, and to undo it.
func TestAdminSetsAndClearsVerification(t *testing.T) {
	env := newSignupEnv(t)

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "locked@example.com", "password": "password123",
	})
	admin := registerAdmin(t, env, "boss@example.com")

	users := doJSON(t, env.handler, http.MethodGet, "/users", admin, nil)
	var directory struct {
		Users []struct {
			ID              string `json:"id"`
			Email           string `json:"email"`
			EmailVerifiedAt any    `json:"emailVerifiedAt"`
		} `json:"users"`
	}
	if err := json.Unmarshal(users.Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	var target string
	for _, u := range directory.Users {
		if u.Email == "locked@example.com" {
			target = u.ID
			if u.EmailVerifiedAt != nil {
				t.Fatal("the account should start unverified")
			}
		}
	}
	if target == "" {
		t.Fatal("the new account is not in the directory")
	}

	verified := doJSON(t, env.handler, http.MethodPatch, "/users/"+target, admin, map[string]any{
		"emailVerified": true,
	})
	if verified.Code != http.StatusOK {
		t.Fatalf("verifying: %d %s", verified.Code, verified.Body.String())
	}
	var afterVerify map[string]any
	if err := json.Unmarshal(verified.Body.Bytes(), &afterVerify); err != nil {
		t.Fatal(err)
	}
	if afterVerify["emailVerifiedAt"] == nil {
		t.Fatal("expected emailVerifiedAt to be set")
	}

	// And clearing it, which is the undo.
	cleared := doJSON(t, env.handler, http.MethodPatch, "/users/"+target, admin, map[string]any{
		"emailVerified": false,
	})
	if cleared.Code != http.StatusOK {
		t.Fatalf("clearing: %d %s", cleared.Code, cleared.Body.String())
	}
	var afterClear map[string]any
	if err := json.Unmarshal(cleared.Body.Bytes(), &afterClear); err != nil {
		t.Fatal(err)
	}
	if afterClear["emailVerifiedAt"] != nil {
		t.Fatalf("expected the stamp to be cleared, got %v", afterClear["emailVerifiedAt"])
	}
}

// Omitting the field must leave verification alone, or every role change would silently
// un-verify somebody.
func TestAdminRoleChangeLeavesVerificationAlone(t *testing.T) {
	env := newSignupEnv(t)

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "steady@example.com", "password": "password123",
	})
	admin := registerAdmin(t, env, "chief@example.com")

	users := doJSON(t, env.handler, http.MethodGet, "/users", admin, nil)
	var directory struct {
		Users []struct {
			ID    string `json:"id"`
			Email string `json:"email"`
		} `json:"users"`
	}
	if err := json.Unmarshal(users.Body.Bytes(), &directory); err != nil {
		t.Fatal(err)
	}
	var target string
	for _, u := range directory.Users {
		if u.Email == "steady@example.com" {
			target = u.ID
		}
	}

	doJSON(t, env.handler, http.MethodPatch, "/users/"+target, admin, map[string]any{
		"emailVerified": true,
	})
	// A patch that says nothing about verification.
	rec := doJSON(t, env.handler, http.MethodPatch, "/users/"+target, admin, map[string]any{
		"isAgent": true,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("patch: %d %s", rec.Code, rec.Body.String())
	}
	var after map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
		t.Fatal(err)
	}
	if after["emailVerifiedAt"] == nil {
		t.Fatalf("verification was cleared by a role change, got %v", after["emailVerifiedAt"])
	}
}

// A non-admin must not be able to verify anybody, including themselves.
func TestNonAdminCannotSetVerification(t *testing.T) {
	env := newSignupEnv(t)

	doJSON(t, env.handler, http.MethodPost, "/auth/register", "", map[string]string{
		"email": "nobody@example.com", "password": "password123",
	})
	login := doJSON(t, env.handler, http.MethodPost, "/auth/login", "", map[string]string{
		"email": "nobody@example.com", "password": "password123",
	})
	var out loginResponse
	if err := json.Unmarshal(login.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	me := doJSON(t, env.handler, http.MethodGet, "/auth/me", out.Token, nil)
	var profile struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(me.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}

	rec := doJSON(t, env.handler, http.MethodPatch, "/users/"+profile.ID, out.Token, map[string]any{
		"emailVerified": true,
	})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", rec.Code, rec.Body.String())
	}
}
