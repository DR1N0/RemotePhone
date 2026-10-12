package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const bootstrapSecretBytes = 32

var bootstrapSecretPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// BootstrapSecret manages the one-time local file used to authorize the
// first-admin setup request. The file, not any network-address check, is
// the real security boundary — see Task 11 (admin-surface routes)'s
// /auth/setup route, which also checks the request's apparent origin as
// defense-in-depth only.
type BootstrapSecret struct {
	path string
}

func NewBootstrapSecret(path string) *BootstrapSecret {
	return &BootstrapSecret{path: path}
}

// GetOrCreate returns the current secret, creating it if no file exists yet.
func (b *BootstrapSecret) GetOrCreate() (string, error) {
	secret, err := b.readExisting()
	if err == nil {
		return secret, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	return b.create()
}

func (b *BootstrapSecret) create() (string, error) {
	raw := make([]byte, bootstrapSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate bootstrap secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)

	if err := os.MkdirAll(filepath.Dir(b.path), 0700); err != nil {
		return "", fmt.Errorf("create bootstrap secret dir: %w", err)
	}

	f, err := os.OpenFile(b.path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		if os.IsExist(err) {
			// Another caller won the creation race; converge on its value.
			return b.readExisting()
		}
		return "", fmt.Errorf("create bootstrap secret file: %w", err)
	}
	defer f.Close()

	if _, err := f.WriteString(secret); err != nil {
		return "", fmt.Errorf("write bootstrap secret: %w", err)
	}
	// Defensive re-chmod in case the process umask affected the mode the
	// file was actually created with.
	if err := os.Chmod(b.path, 0600); err != nil {
		return "", fmt.Errorf("chmod bootstrap secret: %w", err)
	}
	return secret, nil
}

// readExisting strictly validates the file at b.path: must exist, must not
// be a symlink, must be a regular file with permission bits exactly 0600,
// and its content must be a well-formed, canonically-encoded 32-byte
// secret. Any violation other than the file simply not existing is
// returned as an error — callers must not treat a validation failure as
// "doesn't exist" and silently regenerate.
func (b *BootstrapSecret) readExisting() (string, error) {
	info, err := os.Lstat(b.path)
	if err != nil {
		return "", err // preserves os.IsNotExist-detectability
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("bootstrap secret path is a symlink")
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("bootstrap secret path is not a regular file")
	}
	if info.Mode().Perm() != 0600 {
		return "", fmt.Errorf("bootstrap secret file has permissions %o, want exactly 0600", info.Mode().Perm())
	}
	content, err := os.ReadFile(b.path)
	if err != nil {
		return "", fmt.Errorf("read bootstrap secret: %w", err)
	}
	secret := string(content)
	if !bootstrapSecretPattern.MatchString(secret) {
		return "", errors.New("bootstrap secret file content is malformed")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(secret)
	if err != nil || len(decoded) != bootstrapSecretBytes {
		return "", errors.New("bootstrap secret does not decode to 32 bytes")
	}
	if base64.RawURLEncoding.EncodeToString(decoded) != secret {
		return "", errors.New("bootstrap secret is not canonically encoded")
	}
	return secret, nil
}

// Matches reports whether candidate equals the current secret, comparing
// in constant time. It lazily creates the secret file if none exists yet.
func (b *BootstrapSecret) Matches(candidate string) (bool, error) {
	secret, err := b.GetOrCreate()
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare([]byte(candidate), []byte(secret)) == 1, nil
}

// Invalidate deletes the secret file, but only if it still matches secret
// and only if it still exists — both checks make this safe to call
// idempotently and safe against deleting a value that was concurrently
// regenerated to something else.
func (b *BootstrapSecret) Invalidate(secret string) error {
	current, err := b.readExisting()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if subtle.ConstantTimeCompare([]byte(current), []byte(secret)) != 1 {
		return nil
	}
	return os.Remove(b.path)
}
