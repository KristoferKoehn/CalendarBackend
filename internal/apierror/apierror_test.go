package apierror

import "testing"

func TestIsCode(t *testing.T) {
	if !IsCode(NotFound("x"), CodeNotFound) {
		t.Fatal("expected NotFound to match CodeNotFound")
	}
	if IsCode(NotFound("x"), CodeConflict) {
		t.Fatal("did not expect NotFound to match CodeConflict")
	}
	if IsCode(nil, CodeNotFound) {
		t.Fatal("nil should not match")
	}
}

func TestEnvelope(t *testing.T) {
	e := Conflict("Overlapping booking")
	env := e.Envelope()
	if env.Error.Code != CodeConflict || env.Error.Message != "Overlapping booking" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	if env.Error.Details != nil {
		t.Fatal("details should be nil")
	}
}
