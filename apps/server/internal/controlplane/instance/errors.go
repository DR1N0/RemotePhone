package instance

import (
	"errors"
	"fmt"
)

// Sentinel errors returned by Service methods. Compare with errors.Is.
var (
	ErrNotFound       = errors.New("managed instance not found")
	ErrForbidden      = errors.New("user may not manage this instance")
	ErrInvalidRequest = errors.New("invalid instance request")
)

// ErrAuditTerminalOutcomeUnavailable is returned when a worker lifecycle
// call (start/stop) succeeded against the worker, but BOTH persisting that
// outcome AND writing a best-effort "failure" audit event afterward failed.
// That combination means the worker's real state has diverged from the
// database with no audit trail of it at all — ports instance-service.ts's
// AuditTerminalOutcomeUnavailableError: this must surface as a distinct,
// loud condition, never collapse into an ordinary persistence error that
// looks routine.
type ErrAuditTerminalOutcomeUnavailable struct {
	PersistenceErr error
	AuditErr       error
}

func (e *ErrAuditTerminalOutcomeUnavailable) Error() string {
	return fmt.Sprintf(
		"worker operation succeeded, but persisting the result failed (%v) and the fallback failure-audit write also failed (%v)",
		e.PersistenceErr, e.AuditErr,
	)
}

func (e *ErrAuditTerminalOutcomeUnavailable) Unwrap() error { return e.PersistenceErr }
