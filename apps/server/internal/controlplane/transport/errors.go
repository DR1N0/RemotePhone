package transport

import "fmt"

// ErrTransportUnavailable wraps any worker-call failure encountered while
// establishing, tearing down, or signaling a transport — ports
// transport-service.ts's TransportUnavailableError. Validation/authorization
// failures surface as session/instance package sentinels instead; this is
// reserved for "the worker itself is unreachable or errored."
type ErrTransportUnavailable struct {
	Err error
}

func (e *ErrTransportUnavailable) Error() string {
	return fmt.Sprintf("transport unavailable: %v", e.Err)
}

func (e *ErrTransportUnavailable) Unwrap() error { return e.Err }

// ErrAuditTerminalOutcomeUnavailable mirrors instance.ErrAuditTerminalOutcomeUnavailable
// (see its doc comment) for TransitionAttachmentRole's step 6: the worker
// already confirmed targetRole, but persisting that fact AND the fallback
// failure-audit write both failed. Package-local rather than imported from
// instance — every package in this codebase defines its own sentinel error
// ladder rather than sharing one across packages.
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
