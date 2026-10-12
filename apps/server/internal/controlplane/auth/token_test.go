package auth

import (
	"regexp"
	"testing"
)

var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func TestGenerateTokenShapeAndUniqueness(t *testing.T) {
	a, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if !tokenPattern.MatchString(a) {
		t.Errorf("GenerateToken() = %q, want 43-char base64url", a)
	}
	b, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if a == b {
		t.Error("two GenerateToken calls produced the same value")
	}
}

func TestHashTokenIsDeterministicAndHex(t *testing.T) {
	hashPattern := regexp.MustCompile(`^[0-9a-f]{64}$`)
	h1 := HashToken("some-token-value")
	h2 := HashToken("some-token-value")
	if h1 != h2 {
		t.Error("HashToken is not deterministic for the same input")
	}
	if !hashPattern.MatchString(h1) {
		t.Errorf("HashToken() = %q, want 64 lowercase hex chars", h1)
	}
}

func TestHashTokenDiffersForDifferentInput(t *testing.T) {
	if HashToken("token-a") == HashToken("token-b") {
		t.Error("HashToken produced the same output for different input")
	}
}
