package transport

import (
	"context"
	"database/sql"
	"fmt"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence/repo"
	"mako/internal/controlplane/session"
	workerapi "mako/internal/worker/api"
)

const (
	actionTransportCreate     = "transport.create"
	actionTransportTeardown   = "transport.teardown"
	actionTransportRoleChange = "transport.role.update"
	targetTypeAttachment      = "attachment"
)

// Service needs db directly (the same *sql.DB session.Service and
// auth.Service were constructed with) because TransitionAttachmentRole
// combines a session-package DB mutation with an audit record in its own
// transaction — see docs/plans/session-transport-redesign.md.
type Service struct {
	db           *sql.DB
	q            *repo.Queries
	sessionSvc   *session.Service
	instanceSvc  *instance.Service
	workerClient instance.WorkerClient
	keyedMutex   *session.KeyedMutex
}

// NewService constructs a Service. keyedMutex MUST be the same instance
// passed to session.NewService — cross-package request-time serialization
// for the same attachment only works if both packages lock the same map.
func NewService(
	db *sql.DB,
	sessionSvc *session.Service,
	instanceSvc *instance.Service,
	workerClient instance.WorkerClient,
	keyedMutex *session.KeyedMutex,
) *Service {
	return &Service{
		db:           db,
		q:            repo.New(db),
		sessionSvc:   sessionSvc,
		instanceSvc:  instanceSvc,
		workerClient: workerClient,
		keyedMutex:   keyedMutex,
	}
}

func (t *Service) withTx(ctx context.Context, fn func(q *repo.Queries) error) error {
	tx, err := t.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(repo.New(tx)); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// findAttachment mirrors transport-service.ts's getRequiredAttachment:
// it searches the already-fetched SessionRecord's attachments, rather than
// re-querying the DB (session.Service's own lookup does that; this package
// always has the session record in hand already).
func findAttachment(record session.SessionRecord, attachmentID string) (session.AttachmentRecord, error) {
	for _, a := range record.Attachments {
		if a.ID == attachmentID {
			return a, nil
		}
	}
	return session.AttachmentRecord{}, fmt.Errorf("%w: %s", session.ErrAttachmentNotFound, attachmentID)
}

func (t *Service) getSessionAndAttachment(ctx context.Context, sessionID, attachmentID string) (session.SessionRecord, session.AttachmentRecord, error) {
	sessionRecord, err := t.sessionSvc.GetSessionUnchecked(ctx, sessionID)
	if err != nil {
		return session.SessionRecord{}, session.AttachmentRecord{}, err
	}
	attachment, err := findAttachment(sessionRecord, attachmentID)
	if err != nil {
		return session.SessionRecord{}, session.AttachmentRecord{}, err
	}
	return sessionRecord, attachment, nil
}

// recordTerminalPersistenceFailure mirrors instance.Service's method of the
// same name — see its doc comment.
func (t *Service) recordTerminalPersistenceFailure(ctx context.Context, actor audit.Context, attachmentID string, persistenceErr error) error {
	if auditErr := audit.Record(ctx, t.q, actor, actionTransportRoleChange, targetTypeAttachment, attachmentID, audit.OutcomeFailure); auditErr != nil {
		return &ErrAuditTerminalOutcomeUnavailable{PersistenceErr: persistenceErr, AuditErr: auditErr}
	}
	return persistenceErr
}

func (t *Service) destroyAttachmentTransportUnlocked(ctx context.Context, sessionID, attachmentID string, actor audit.Context) error {
	sessionRecord, attachment, err := t.getSessionAndAttachment(ctx, sessionID, attachmentID)
	if err != nil {
		return err
	}
	instanceRecord, err := t.instanceSvc.GetManagedInstanceUnchecked(ctx, sessionRecord.InstanceID)
	if err != nil {
		return err
	}

	if err := audit.Record(ctx, t.q, actor, actionTransportTeardown, targetTypeAttachment, attachment.ID, audit.OutcomeAttempt); err != nil {
		return fmt.Errorf("record attempt audit event: %w", err)
	}
	if err := t.workerClient.DestroyTransportSession(ctx, instanceRecord.ID, attachment.ID); err != nil {
		if auditErr := audit.Record(ctx, t.q, actor, actionTransportTeardown, targetTypeAttachment, attachment.ID, audit.OutcomeFailure); auditErr != nil {
			return auditErr
		}
		return &ErrTransportUnavailable{Err: err}
	}
	return audit.Record(ctx, t.q, actor, actionTransportTeardown, targetTypeAttachment, attachment.ID, audit.OutcomeSuccess)
}

// EnsureAttachmentTransport establishes (or re-establishes, tearing down a
// stale one first) attachmentID's worker-side transport session.
// KeyedMutex-serialized per attachment so a concurrent teardown/role-change
// for the same attachment can't interleave with the worker calls below.
func (t *Service) EnsureAttachmentTransport(ctx context.Context, sessionID, attachmentID string, actor audit.Context) (workerapi.TransportSessionResponse, error) {
	unlock := t.keyedMutex.Lock(session.AttachmentKey(sessionID, attachmentID))
	defer unlock()

	sessionRecord, attachment, err := t.getSessionAndAttachment(ctx, sessionID, attachmentID)
	if err != nil {
		return workerapi.TransportSessionResponse{}, err
	}
	if sessionRecord.Status != "active" {
		return workerapi.TransportSessionResponse{}, fmt.Errorf("%w: session is not transportable: %s", session.ErrInvalidRequest, sessionID)
	}
	if attachment.Status != "connected" {
		return workerapi.TransportSessionResponse{}, fmt.Errorf("%w: attachment is not connected: %s", session.ErrInvalidRequest, attachmentID)
	}
	if attachment.TransportTeardownPending {
		if err := t.destroyAttachmentTransportUnlocked(ctx, sessionID, attachmentID, actor); err != nil {
			return workerapi.TransportSessionResponse{}, err
		}
		if err := t.sessionSvc.CompleteAttachmentTransportTeardown(ctx, sessionID, attachmentID); err != nil {
			return workerapi.TransportSessionResponse{}, err
		}
	}

	instanceRecord, err := t.instanceSvc.GetManagedInstanceUnchecked(ctx, sessionRecord.InstanceID)
	if err != nil {
		return workerapi.TransportSessionResponse{}, err
	}
	if instanceRecord.RuntimeHandle == nil {
		return workerapi.TransportSessionResponse{}, fmt.Errorf("%w: instance is not running: %s", session.ErrInvalidRequest, instanceRecord.ID)
	}

	if err := audit.Record(ctx, t.q, actor, actionTransportCreate, targetTypeAttachment, attachment.ID, audit.OutcomeAttempt); err != nil {
		return workerapi.TransportSessionResponse{}, fmt.Errorf("record attempt audit event: %w", err)
	}
	resp, err := t.workerClient.CreateTransportSession(ctx, workerapi.CreateTransportSessionRequest{
		SessionID:     sessionID,
		AttachmentID:  attachmentID,
		InstanceID:    instanceRecord.ID,
		RuntimeClass:  instanceRecord.RuntimeClass,
		Role:          attachment.EffectiveTransportRole,
		RuntimeHandle: *instanceRecord.RuntimeHandle,
	})
	if err != nil {
		if auditErr := audit.Record(ctx, t.q, actor, actionTransportCreate, targetTypeAttachment, attachment.ID, audit.OutcomeFailure); auditErr != nil {
			return workerapi.TransportSessionResponse{}, auditErr
		}
		return workerapi.TransportSessionResponse{}, &ErrTransportUnavailable{Err: err}
	}
	if err := audit.Record(ctx, t.q, actor, actionTransportCreate, targetTypeAttachment, attachment.ID, audit.OutcomeSuccess); err != nil {
		return workerapi.TransportSessionResponse{}, err
	}
	return resp, nil
}

// DestroyAttachmentTransport tears down attachmentID's worker-side transport
// session unconditionally (KeyedMutex-serialized).
func (t *Service) DestroyAttachmentTransport(ctx context.Context, sessionID, attachmentID string, actor audit.Context) error {
	unlock := t.keyedMutex.Lock(session.AttachmentKey(sessionID, attachmentID))
	defer unlock()
	return t.destroyAttachmentTransportUnlocked(ctx, sessionID, attachmentID, actor)
}

// TeardownPendingAttachment tears down attachmentID's transport only if
// session.Service marked it transport-teardown-pending (e.g. after a
// disconnect), clearing the flag on success. Reports whether a teardown was
// actually performed.
func (t *Service) TeardownPendingAttachment(ctx context.Context, sessionID, attachmentID string, actor audit.Context) (bool, error) {
	unlock := t.keyedMutex.Lock(session.AttachmentKey(sessionID, attachmentID))
	defer unlock()

	_, attachment, err := t.getSessionAndAttachment(ctx, sessionID, attachmentID)
	if err != nil {
		return false, err
	}
	if !attachment.TransportTeardownPending {
		return false, nil
	}
	if err := t.destroyAttachmentTransportUnlocked(ctx, sessionID, attachmentID, actor); err != nil {
		return false, err
	}
	if err := t.sessionSvc.CompleteAttachmentTransportTeardown(ctx, sessionID, attachmentID); err != nil {
		return false, err
	}
	return true, nil
}

// TransitionAttachmentRole is the single promote/demote replacement
// described in docs/plans/session-transport-redesign.md: one method, same
// shape for both directions, owning persist+worker-sync+retry internally
// instead of routes/sessions.ts's four-times-patched, differently-ordered
// handlers (whose promote path had a real role/worker-state race — the DB
// committed role=participant before the worker was told).
//
//  1. Authorize caller as the session's moderator; reject a closed session
//     or an attempt to change the owner attachment's role.
//  2. Mark pending FIRST, always, before touching the worker — one
//     transaction sets pending_target_role and records an attempt audit
//     event. EffectiveTransportRole immediately starts reporting "observer"
//     for this attachment, closing the race: nothing ever reports a role
//     the worker hasn't been asked to enforce yet.
//  3. Call the worker with the real targetRole (not "observer").
//  4. On success: one transaction confirms role=targetRole, clears
//     pending_target_role, records a success audit event.
//  5. On worker failure: best-effort re-call the worker targeting
//     "observer" explicitly (defensive — the failed update may have left
//     it in an undefined state), swallowing a second failure.
//     pending_target_role is left set — never cleared on this path — and a
//     failure audit event is recorded. Returns the original error.
//  6. If step 4's transaction itself fails (worker succeeded, DB didn't):
//     ErrAuditTerminalOutcomeUnavailable, the same escape hatch instance.Service
//     uses for the identical worker/DB divergence shape.
func (t *Service) TransitionAttachmentRole(
	ctx context.Context,
	sessionID, attachmentID, targetRole string,
	caller auth.AuthenticatedUser,
	actor audit.Context,
) (session.AttachmentRecord, error) {
	if err := t.sessionSvc.AuthorizeSessionModeration(ctx, sessionID, caller); err != nil {
		return session.AttachmentRecord{}, err
	}
	sessionRecord, attachment, err := t.getSessionAndAttachment(ctx, sessionID, attachmentID)
	if err != nil {
		return session.AttachmentRecord{}, err
	}
	if sessionRecord.Status != "active" {
		return session.AttachmentRecord{}, fmt.Errorf("%w: session is closed: %s", session.ErrInvalidRequest, sessionID)
	}
	if attachment.Role == "owner" {
		return session.AttachmentRecord{}, fmt.Errorf("%w: owner attachment cannot change role: %s", session.ErrInvalidRequest, attachmentID)
	}

	unlock := t.keyedMutex.Lock(session.AttachmentKey(sessionID, attachmentID))
	defer unlock()

	// Step 2.
	pendingRole := targetRole
	if err := t.withTx(ctx, func(q *repo.Queries) error {
		if _, err := t.sessionSvc.SetAttachmentPendingTargetRole(ctx, q, sessionID, attachmentID, &pendingRole); err != nil {
			return err
		}
		return audit.Record(ctx, q, actor, actionTransportRoleChange, targetTypeAttachment, attachmentID, audit.OutcomeAttempt)
	}); err != nil {
		return session.AttachmentRecord{}, err
	}

	instanceRecord, err := t.instanceSvc.GetManagedInstanceUnchecked(ctx, sessionRecord.InstanceID)
	if err != nil {
		return session.AttachmentRecord{}, err
	}

	// Step 3.
	if err := t.workerClient.UpdateTransportRole(ctx, workerapi.UpdateTransportSessionRoleRequest{
		InstanceID:   instanceRecord.ID,
		AttachmentID: attachmentID,
		Role:         targetRole,
	}); err != nil {
		// Step 5.
		_ = t.workerClient.UpdateTransportRole(ctx, workerapi.UpdateTransportSessionRoleRequest{
			InstanceID:   instanceRecord.ID,
			AttachmentID: attachmentID,
			Role:         "observer",
		})
		if auditErr := audit.Record(ctx, t.q, actor, actionTransportRoleChange, targetTypeAttachment, attachmentID, audit.OutcomeFailure); auditErr != nil {
			return session.AttachmentRecord{}, auditErr
		}
		return session.AttachmentRecord{}, &ErrTransportUnavailable{Err: err}
	}

	// Step 4.
	var result session.AttachmentRecord
	txErr := t.withTx(ctx, func(q *repo.Queries) error {
		confirmed, err := t.sessionSvc.ConfirmAttachmentRole(ctx, q, sessionID, attachmentID, targetRole)
		if err != nil {
			return err
		}
		if err := audit.Record(ctx, q, actor, actionTransportRoleChange, targetTypeAttachment, attachmentID, audit.OutcomeSuccess); err != nil {
			return err
		}
		result = confirmed
		return nil
	})
	if txErr != nil {
		// Step 6.
		return session.AttachmentRecord{}, t.recordTerminalPersistenceFailure(ctx, actor, attachmentID, txErr)
	}
	return result, nil
}

// RelayOffer and RelayICECandidate are thin, unauthorized pass-throughs to
// the worker — ports signaling-service.ts exactly, which has zero
// authorization logic of its own. Authorizing the caller against
// instanceId/attachmentId is the route layer's job (task #26), same as TS's
// routes/signaling.ts.
func (t *Service) RelayOffer(ctx context.Context, instanceID, attachmentID, sdpOffer string) (string, error) {
	resp, err := t.workerClient.ForwardOffer(ctx, workerapi.TransportOfferRequest{
		InstanceID:   instanceID,
		AttachmentID: attachmentID,
		SdpOffer:     sdpOffer,
	})
	if err != nil {
		return "", &ErrTransportUnavailable{Err: err}
	}
	return resp.SdpAnswer, nil
}

func (t *Service) RelayICECandidate(ctx context.Context, instanceID, attachmentID, candidate string) error {
	if err := t.workerClient.ForwardICECandidate(ctx, workerapi.ICECandidateRequest{
		InstanceID:   instanceID,
		AttachmentID: attachmentID,
		Candidate:    candidate,
	}); err != nil {
		return &ErrTransportUnavailable{Err: err}
	}
	return nil
}
