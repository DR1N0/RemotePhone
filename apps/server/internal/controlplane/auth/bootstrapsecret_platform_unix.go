//go:build !windows

package auth

import (
	"fmt"
	"os"
	"syscall"
)

// openSecretFile opens path without following a final-component symlink
// (O_NOFOLLOW). A symlink swapped in after any earlier path check therefore
// fails the open itself (ELOOP) instead of being silently followed.
func openSecretFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
}

// validateSecretFileSecurity enforces the POSIX invariant this secret file
// is created with: mode exactly 0600. Windows has no equivalent permission
// bits — see bootstrapsecret_platform_windows.go.
func validateSecretFileSecurity(info os.FileInfo) error {
	if info.Mode().Perm() != 0600 {
		return fmt.Errorf("bootstrap secret file has permissions %o, want exactly 0600", info.Mode().Perm())
	}
	return nil
}
