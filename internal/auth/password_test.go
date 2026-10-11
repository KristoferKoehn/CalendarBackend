package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	encoded, err := HashPassword("password123")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword("password123", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected correct password to verify")
	}

	ok, err = VerifyPassword("wrong-password", encoded)
	if err != nil {
		t.Fatalf("VerifyPassword wrong: %v", err)
	}
	if ok {
		t.Fatal("expected wrong password to fail")
	}
}

func TestHashPasswordUniqueSalt(t *testing.T) {
	a, err := HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("password123")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected distinct hashes for the same password")
	}
}

func TestVerifyPasswordMalformed(t *testing.T) {
	if _, err := VerifyPassword("x", "not-a-valid-hash"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}
