package auth

import (
	"strings"
	"testing"
)

func TestHashAndVerifyPasswordRoundTrip(t *testing.T) {
	encoded, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword("correct horse battery staple", encoded) {
		t.Error("VerifyPassword() = false, want true for correct password")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	encoded, _ := HashPassword("correct horse battery staple")
	if VerifyPassword("wrong password", encoded) {
		t.Error("VerifyPassword() = true, want false for wrong password")
	}
}

func TestHashPasswordUsesFreshSaltEachCall(t *testing.T) {
	a, _ := HashPassword("same password")
	b, _ := HashPassword("same password")
	if a == b {
		t.Error("two HashPassword calls with the same input produced identical output — salt is not random")
	}
}

func TestVerifyPasswordRejectsMalformedInput(t *testing.T) {
	cases := map[string]string{
		"empty string":         "",
		"garbage":              "not-a-hash-at-all",
		"too few fields":       "scrypt-v1$16384$8$1$64$onlysalt",
		"non-numeric param":    "scrypt-v1$abc$8$1$64$c2FsdA$a2V5",
		"wrong version string": "bcrypt-v1$16384$8$1$64$c2FsdA$a2V5",
	}
	for name, encoded := range cases {
		t.Run(name, func(t *testing.T) {
			if VerifyPassword("anything", encoded) {
				t.Errorf("VerifyPassword(%q) = true, want false", encoded)
			}
		})
	}
}

func TestVerifyPasswordRejectsCostAboveCeiling(t *testing.T) {
	encoded, err := HashPassword("some password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	// Replace the declared cost (16384) with one over today's ceiling (16385).
	tampered := strings.Replace(encoded, "$16384$", "$16385$", 1)
	if tampered == encoded {
		t.Fatal("test setup bug: replacement did not change the string")
	}
	if VerifyPassword("some password", tampered) {
		t.Error("VerifyPassword() = true for a cost above today's ceiling, want false")
	}
}
