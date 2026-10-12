package auth

import "errors"

// Sentinel errors returned by AuthService methods. Compare with errors.Is,
// not type assertion or string matching — ServiceError (below) wraps some
// of these with additional ad hoc context for cases the TS implementation
// used a single generic error for.
var (
	ErrServerAlreadyClaimed       = errors.New("server has already been claimed")
	ErrBootstrapSecretInvalid     = errors.New("invalid bootstrap secret")
	ErrBootstrapSecretUnavailable = errors.New("bootstrap secret is not configured")
	ErrInvalidLogin               = errors.New("invalid username or password")
	ErrUserNotFound               = errors.New("user not found")
	ErrUserBanned                 = errors.New("user is banned")
	ErrCredentialNotFound         = errors.New("credential not found")
	ErrCredentialNotOwned         = errors.New("credential belongs to a different user")
	ErrRegistrationInviteInvalid  = errors.New("registration invite is invalid or expired")
	ErrUsernameConflict           = errors.New("username is already registered")
	ErrNotAdmin                   = errors.New("caller is not an active admin")
)

// ServiceError carries an ad hoc message for a validation failure that the
// TS implementation represented as a generic, undifferentiated
// AuthServiceError (e.g. "guest users cannot receive account credentials",
// "attachment credential expiration must be within 24 hours", "a credential
// can only be revoked by its own user" is NOT this — that specific case has
// its own sentinel, ErrCredentialNotOwned, above). Callers needing to
// distinguish a specific ServiceError condition should give it its own
// sentinel instead of matching on Message.
type ServiceError struct {
	Message string
}

func (e *ServiceError) Error() string { return e.Message }

func NewServiceError(message string) error { return &ServiceError{Message: message} }
