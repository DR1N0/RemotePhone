package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// GenerateToken returns a fresh high-entropy bearer token: 32 random bytes,
// base64url-encoded without padding (43 characters, 256 bits of entropy).
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken returns the lowercase hex SHA-256 digest of token, used as the
// lookup key in the credentials table. Unsalted: the input is already
// high-entropy random data, not a low-entropy secret, so there is no
// dictionary-attack surface a salt would protect against.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
