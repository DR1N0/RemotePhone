package image

import "errors"

// Sentinel errors returned by Service methods. Compare with errors.Is.
var (
	ErrNotFound       = errors.New("base image or template not found")
	ErrForbidden      = errors.New("user may not perform this image operation")
	ErrInvalidRequest = errors.New("invalid image request")
)
