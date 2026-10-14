package session

import "errors"

// Sentinel errors returned by Service methods. Compare with errors.Is.
var (
	ErrSessionNotFound      = errors.New("session not found")
	ErrAttachmentNotFound   = errors.New("attachment not found")
	ErrSessionConflict      = errors.New("active session already exists for this instance")
	ErrInvalidRequest       = errors.New("invalid session request")
	ErrForbidden            = errors.New("caller may not perform this session operation")
	ErrSessionInviteInvalid = errors.New("session invite is invalid or expired")
)
