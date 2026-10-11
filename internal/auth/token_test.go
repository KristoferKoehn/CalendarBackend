package auth

import "testing"

func TestNewToken(t *testing.T) {
	a, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if a == "" || b == "" {
		t.Fatal("token must not be empty")
	}
	if a == b {
		t.Fatal("tokens should be unique")
	}
}

func TestHashToken(t *testing.T) {
	raw, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if HashToken(raw) == raw {
		t.Fatal("hash must differ from raw token")
	}
	if HashToken(raw) != HashToken(raw) {
		t.Fatal("hash must be deterministic")
	}
}
