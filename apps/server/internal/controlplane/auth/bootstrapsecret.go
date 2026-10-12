package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
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

// create generates a fresh secret and publishes it at b.path.
//
// The secret is written in full to a private temp file in the same
// directory first, then published atomically via os.Link (a hard link
// succeeds only if b.path does not already exist, and — unlike
// O_CREATE|O_EXCL on b.path directly — it can never expose a reader to a
// zero-byte or partially written file: the inode b.path ends up pointing
// at already contains the complete secret before the link is created).
// This closes a race where a concurrent create() creates-then-writes
// b.path directly: a loser of the creation race falling back to
// readExisting() could otherwise observe the winner's file in the
// window after O_CREATE|O_EXCL but before the write completed, see
// empty/partial content, and fail instead of converging.
func (b *BootstrapSecret) create() (string, error) {
	raw := make([]byte, bootstrapSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate bootstrap secret: %w", err)
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)

	dir := filepath.Dir(b.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create bootstrap secret dir: %w", err)
	}

	// Temp file lives in the same directory as b.path so the later
	// os.Link (which requires both names to be on the same filesystem)
	// is guaranteed to work.
	tmp, err := os.CreateTemp(dir, ".bootstrap-secret-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create bootstrap secret temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // best-effort cleanup; no-op once already removed below

	if _, err := tmp.WriteString(secret); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write bootstrap secret: %w", err)
	}
	// Defensive chmod in case the process umask affected the mode the
	// temp file was actually created with: the published file must be
	// exactly 0600 regardless of umask.
	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("chmod bootstrap secret temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close bootstrap secret temp file: %w", err)
	}

	if err := os.Link(tmpPath, b.path); err != nil {
		if os.IsExist(err) {
			// Another caller won the creation race; converge on its value.
			return b.readExisting()
		}
		return "", fmt.Errorf("publish bootstrap secret file: %w", err)
	}
	return secret, nil
}

// readExisting strictly validates the file at b.path: must exist, must not
// be a symlink, must be a regular file with permission bits exactly 0600,
// and its content must be a well-formed, canonically-encoded 32-byte
// secret. Any violation other than the file simply not existing is
// returned as an error — callers must not treat a validation failure as
// "doesn't exist" and silently regenerate.
//
// The file is opened once with O_NOFOLLOW, and every subsequent check
// (regular-file, exact-permission) plus the actual read is performed
// against that same open file descriptor rather than by re-resolving
// b.path a second time. This closes the TOCTOU window that a separate
// os.Lstat-then-os.ReadFile pair would leave open: a symlink swapped in
// between the two path resolutions would otherwise be followed silently
// by os.ReadFile despite an earlier Lstat rejecting it.
func (b *BootstrapSecret) readExisting() (string, error) {
	f, err := os.OpenFile(b.path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err // preserves os.IsNotExist-detectability for ENOENT;
		// a symlink at the final component fails with ELOOP here, which is
		// NOT os.IsNotExist, so it correctly propagates as a real error
		// rather than triggering regeneration
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("stat bootstrap secret: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("bootstrap secret path is not a regular file")
	}
	if info.Mode().Perm() != 0600 {
		return "", fmt.Errorf("bootstrap secret file has permissions %o, want exactly 0600", info.Mode().Perm())
	}
	content, err := io.ReadAll(f)
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
