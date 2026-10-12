// Package auth implements mako's bearer-credential identity system: password
// hashing, token generation, the bootstrap secret, and the AuthService that
// ties them to persisted users and credentials. This is a faithful port of
// the TypeScript implementation in apps/controlplane/src/services/{auth-service,
// password,token,bootstrap-secret}.ts — exact parameters and boundary
// conditions are intentional, not incidental, and must not be "improved"
// without discussing with a human first.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

const (
	scryptVersion = "scrypt-v1"
	scryptN       = 16384
	scryptR       = 8
	scryptP       = 1
	scryptKeyLen  = 64
	saltLen       = 16
)

// HashPassword derives a scrypt key from password with a fresh random salt
// and returns the self-describing encoded form
// "scrypt-v1$N$r$p$keyLen$salt$key" (salt/key base64url, no padding).
func HashPassword(password string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptKeyLen)
	if err != nil {
		return "", fmt.Errorf("derive key: %w", err)
	}
	return fmt.Sprintf("%s$%d$%d$%d$%d$%s$%s",
		scryptVersion, scryptN, scryptR, scryptP, scryptKeyLen,
		base64.RawURLEncoding.EncodeToString(salt),
		base64.RawURLEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword reports whether password matches the encoded hash,
// re-deriving the key with the hash's OWN embedded parameters. It never
// panics and never returns an error — any malformed input, or a hash
// declaring parameters above today's constants, yields false.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 7 {
		return false
	}
	if parts[0] != scryptVersion {
		return false
	}
	cost, okCost := parsePositiveIntCeiling(parts[1], scryptN)
	blockSize, okBlock := parsePositiveIntCeiling(parts[2], scryptR)
	parallelization, okPar := parsePositiveIntCeiling(parts[3], scryptP)
	keyLength, okKeyLen := parsePositiveIntCeiling(parts[4], scryptKeyLen)
	if !okCost || !okBlock || !okPar || !okKeyLen {
		return false
	}
	salt, err := base64.RawURLEncoding.DecodeString(parts[5])
	if err != nil || len(salt) != saltLen {
		return false
	}
	key, err := base64.RawURLEncoding.DecodeString(parts[6])
	if err != nil || len(key) != keyLength {
		return false
	}
	derived, err := scrypt.Key([]byte(password), salt, cost, blockSize, parallelization, keyLength)
	if err != nil {
		return false
	}
	return len(derived) == len(key) && subtle.ConstantTimeCompare(derived, key) == 1
}

// parsePositiveIntCeiling parses s as a positive integer and reports it
// valid only if it is <= ceiling — a hash declaring a parameter higher than
// today's corresponding constant is rejected, not honored, so lowering a
// constant in a future release invalidates old hashes rather than silently
// verifying them at the old, more expensive setting.
func parsePositiveIntCeiling(s string, ceiling int) (int, bool) {
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 || n > ceiling {
		return 0, false
	}
	return n, true
}
