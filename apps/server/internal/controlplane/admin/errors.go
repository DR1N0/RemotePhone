package admin

import "errors"

// Sentinel errors specific to admin operations. "not an admin" / "user not
// found" cases reuse auth.ErrNotAdmin / auth.ErrUserNotFound directly
// instead of redefining equivalents here.
var (
	ErrLastActiveAdmin           = errors.New("cannot remove the final active admin")
	ErrCannotRemoveSystem        = errors.New("cannot remove the system owner")
	ErrGuestAttachmentNotFound   = errors.New("guest attachment not found")
	ErrAccountCredentialNotFound = errors.New("account credential not found")
)
