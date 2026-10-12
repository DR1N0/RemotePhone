package auth

import (
	"errors"
	"testing"
)

func TestSentinelErrorsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrServerAlreadyClaimed, ErrBootstrapSecretInvalid, ErrBootstrapSecretUnavailable,
		ErrInvalidLogin, ErrUserNotFound, ErrUserBanned, ErrCredentialNotFound,
		ErrCredentialNotOwned, ErrRegistrationInviteInvalid, ErrUsernameConflict,
		ErrNotAdmin,
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i == j {
				continue
			}
			if errors.Is(a, b) {
				t.Errorf("sentinel %d (%v) is errors.Is sentinel %d (%v), want distinct", i, a, j, b)
			}
		}
	}
}

func TestServiceErrorCarriesMessage(t *testing.T) {
	err := NewServiceError("guest users cannot receive account credentials")
	if err.Error() != "guest users cannot receive account credentials" {
		t.Errorf("Error() = %q, want the exact message passed in", err.Error())
	}
	var svcErr *ServiceError
	if !errors.As(err, &svcErr) {
		t.Error("errors.As(err, *ServiceError) failed")
	}
}
