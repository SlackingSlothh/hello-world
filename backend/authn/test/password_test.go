package authn_test

import (
	"testing"

	"hello-world/authn"
)

func TestPasswordHashEncodeParseAndVerify(t *testing.T) {
	password := "test-password with spaces"
	first, err := authn.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	second, err := authn.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := first.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	other, err := second.Encoded()
	if err != nil {
		t.Fatal(err)
	}
	if encoded == other {
		t.Fatal("repeated password must receive different salt")
	}
	parsed, err := authn.ParsePasswordHash(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Matches(password) || parsed.Matches("wrong") || parsed.Matches("") {
		t.Fatal("password verification mismatch")
	}
	roundTrip, err := parsed.Encoded()
	if err != nil || roundTrip != encoded {
		t.Fatal("PHC encoding did not round trip")
	}
}

func TestPasswordHashRejectsInvalidInput(t *testing.T) {
	if _, err := authn.HashPassword(""); err == nil {
		t.Fatal("empty password accepted")
	}
	if _, err := authn.ParsePasswordHash("not-a-password-hash"); err == nil {
		t.Fatal("invalid password hash accepted")
	}
}
