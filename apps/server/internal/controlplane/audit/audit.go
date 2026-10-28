// Package audit holds the shared audit-event context/recording helper used
// by instance, session, and admin, plus (from Phase 3 onward) audit-log
// retrieval for the admin package's GET /api/v1/admin/audit route. Read and
// write both live here, in one package, matching how every other table's
// reads/writes stay owned by one package in this codebase (e.g. session
// owns all sessions/attachments access). auth/service.go predates this
// package and still writes audit_events rows inline via repo.CreateAuditEvent
// directly; that's left as-is rather than churned for consistency's sake.
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"remotephone/internal/controlplane/persistence/repo"

	"github.com/google/uuid"
)

// ErrInvalidCursor is returned by List when cursor doesn't match any
// existing audit event — mirrors TS's InvalidAuditCursorError.
var ErrInvalidCursor = errors.New("invalid audit cursor")

// ErrInvalidLimit is returned by List when limit falls outside [1, 100] —
// mirrors TS audit-events.ts's RangeError. adminapi's handler also validates
// this before calling in, but List re-validates independently rather than
// trusting every future caller to have done so.
var ErrInvalidLimit = errors.New("audit limit must be an integer from 1 to 100")

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

// Event is the only shape an audit_events row may take crossing into an
// HTTP response — the audit-log equivalent of auth.UserDTO.
type Event struct {
	ID                string  `json:"id"`
	ActorUserID       *string `json:"actorUserId"`
	ActorCredentialID *string `json:"actorCredentialId"`
	Action            string  `json:"action"`
	TargetType        string  `json:"targetType"`
	TargetID          string  `json:"targetId"`
	RequestID         string  `json:"requestId"`
	Outcome           string  `json:"outcome"`
	CreatedAt         string  `json:"createdAt"`
}

func toEvent(row repo.AuditEvent) Event {
	var actorUserID, actorCredentialID *string
	if row.ActorUserID.Valid {
		v := row.ActorUserID.String
		actorUserID = &v
	}
	if row.ActorCredentialID.Valid {
		v := row.ActorCredentialID.String
		actorCredentialID = &v
	}
	return Event{
		ID:                row.ID,
		ActorUserID:       actorUserID,
		ActorCredentialID: actorCredentialID,
		Action:            row.Action,
		TargetType:        row.TargetType,
		TargetID:          row.TargetID,
		RequestID:         row.RequestID,
		Outcome:           row.Outcome,
		CreatedAt:         row.CreatedAt,
	}
}

// List returns up to limit audit events, newest first, keyset-paginated by
// cursor (an opaque event id from a previous page's nextCursor). Mirrors
// audit-events.ts's listAuditEvents: cursor, if non-nil, must name an
// existing event or ErrInvalidCursor is returned; nextCursor is non-nil iff
// more pages remain.
func List(ctx context.Context, q *repo.Queries, limit int, cursor *string) ([]Event, *string, error) {
	if limit < 1 || limit > 100 {
		return nil, nil, fmt.Errorf("%w: %d", ErrInvalidLimit, limit)
	}

	var cursorCreatedAt interface{}
	var cursorID sql.NullString
	if cursor != nil {
		row, err := q.GetAuditEventByID(ctx, *cursor)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil, fmt.Errorf("%w: %s", ErrInvalidCursor, *cursor)
			}
			return nil, nil, fmt.Errorf("get audit event by id: %w", err)
		}
		cursorCreatedAt = row.CreatedAt
		cursorID = sql.NullString{String: row.ID, Valid: true}
	}

	rows, err := q.ListAuditEvents(ctx, repo.ListAuditEventsParams{
		CursorCreatedAt: cursorCreatedAt,
		CursorID:        cursorID,
		Limit:           int64(limit) + 1,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("list audit events: %w", err)
	}

	items := make([]Event, 0, len(rows))
	for _, row := range rows {
		items = append(items, toEvent(row))
	}
	var nextCursor *string
	if len(items) > limit {
		items = items[:limit]
		nc := items[len(items)-1].ID
		nextCursor = &nc
	}
	return items, nextCursor, nil
}
