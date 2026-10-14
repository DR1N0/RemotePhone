// Package audit holds the shared audit-event context/recording helper used
// by instance (and, from Phase 2/3 onward, session and admin). It is
// deliberately minimal: just enough to replace instance-service.ts's and
// friends' repeated "write one audit_events row" boilerplate with one
// shared call. Audit-log retrieval (the TS audit-service.ts's list()) is
// not built yet — nothing needs it until Phase 3's admin routes do; adding
// it now would be unused code. auth/service.go predates this package and
// still writes audit_events rows inline via repo.CreateAuditEvent directly;
// that's left as-is rather than churned for consistency's sake.
package audit

import (
	"context"
	"database/sql"
	"fmt"

	"mako/internal/controlplane/persistence/repo"

	"github.com/google/uuid"
)

type Outcome string

const (
	OutcomeAttempt Outcome = "attempt"
	OutcomeSuccess Outcome = "success"
	OutcomeFailure Outcome = "failure"
)

// Context identifies who is performing an action and ties every audit event
// from one HTTP request together via RequestID. ActorUserID/ActorCredentialID
// are nil for unauthenticated actions (e.g. registration), mirroring the TS
// AuditContext's nullable actor fields.
type Context struct {
	ActorUserID       *string
	ActorCredentialID *string
	RequestID         string
}

func NewContext(actorUserID, actorCredentialID *string, requestID string) Context {
	return Context{ActorUserID: actorUserID, ActorCredentialID: actorCredentialID, RequestID: requestID}
}

// Record writes one audit_events row via q. Callers choose whether q is
// scoped to an existing transaction (so the event commits atomically with
// the operation it documents, e.g. an instance-lifecycle update) or a
// fresh one-off query (for pre-operation "attempt" events and best-effort
// post-failure logging, which can't share a transaction with a network
// call to a worker).
func Record(ctx context.Context, q *repo.Queries, actor Context, action, targetType, targetID string, outcome Outcome) error {
	params := repo.CreateAuditEventParams{
		ID:         uuid.NewString(),
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		RequestID:  actor.RequestID,
		Outcome:    string(outcome),
	}
	if actor.ActorUserID != nil {
		params.ActorUserID = sql.NullString{String: *actor.ActorUserID, Valid: true}
	}
	if actor.ActorCredentialID != nil {
		params.ActorCredentialID = sql.NullString{String: *actor.ActorCredentialID, Valid: true}
	}
	if err := q.CreateAuditEvent(ctx, params); err != nil {
		return fmt.Errorf("create audit event: %w", err)
	}
	return nil
}
