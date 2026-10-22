//go:build windows

package auth

import (
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

// openSecretFile opens path for reading while refusing a reparse point
// (symlink or junction) at the final component. Windows has no O_NOFOLLOW
// exposed by os.OpenFile, so the check is done with GetFileAttributes before
// the open. That leaves the same narrow TOCTOU window Windows offers no
// handle-level flag to close; the secret still lives in a per-user profile
// directory (or an operator-chosen directory) whose ACL is the real barrier,
// and creating a symlink/junction requires a privilege normal users don't
// hold.
func openSecretFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, fmt.Errorf("encode bootstrap secret path: %w", err)
	}
	attrs, err := windows.GetFileAttributes(pathPtr)
	if err != nil {
		return nil, err // preserves os.IsNotExist(ENOENT) behavior for callers
	}
	if attrs&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return nil, fmt.Errorf("bootstrap secret path is a reparse point (symlink or junction)")
	}
	return os.Open(path)
}

// validateSecretFileSecurity is a no-op on Windows: os.FileMode permission
// bits are synthesized (typically 0666/0444) and cannot prove that only the
// owning user can read the file. The security boundary there is the NTFS
// ACL the file inherits from its directory — %LOCALAPPDATA% or the database
// directory under the user profile — not a POSIX mode. Tightening that ACL
// further is a deployment concern, not a startup check.
func validateSecretFileSecurity(info os.FileInfo) error { return nil }
