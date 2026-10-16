package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/persistence/repo"

	"github.com/google/uuid"
)

const (
	actionSessionClose                = "session.close"
	actionSessionAttachmentDisconnect = "session.attachment.disconnect"
	targetTypeSession                 = "session"
	targetTypeAttachment              = "attachment"
)

const attachmentCredentialTTL = 24 * time.Hour

const (
	ownerAbsenceTimeoutMin = 30 * time.Second
	ownerAbsenceTimeoutMax = time.Hour
)

type Service struct {
	db                         *sql.DB
	q                          *repo.Queries
	authSvc                    *auth.Service
	keyedMutex                 *KeyedMutex
	now                        func() time.Time
	defaultOwnerAbsenceTimeout time.Duration
}

// NewService constructs a Service backed by db, which MUST be the same
// *sql.DB auth.NewService was constructed with: CreateSession,
// DisconnectAttachment, and ResumeAttachment call authSvc's *Tx methods
// from inside their own transaction, which only works against the same
// connection pool (see auth.IssueAttachmentCredentialTx's doc comment).
// keyedMutex MUST also be shared with transport.Service — it is the only
// thing serializing this package's and transport's worker-adjacent calls
// against each other for the same attachment (see keyed_mutex.go).
func NewService(db *sql.DB, authSvc *auth.Service, keyedMutex *KeyedMutex, defaultOwnerAbsenceTimeout time.Duration) *Service {
	return &Service{
		db:                         db,
		q:                          repo.New(db),
		authSvc:                    authSvc,
		keyedMutex:                 keyedMutex,
		now:                        time.Now,
		defaultOwnerAbsenceTimeout: defaultOwnerAbsenceTimeout,
	}
}

func (s *Service) withTx(ctx context.Context, fn func(q *repo.Queries) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
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

func formatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func nullStringFromPtr(v *string) sql.NullString {
	if v == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

// clampOwnerAbsenceTimeoutMs mirrors session-service.ts's
// clampOwnerAbsenceTimeoutMs.
func clampOwnerAbsenceTimeoutMs(ms int64) int64 {
	d := time.Duration(ms) * time.Millisecond
	if d < ownerAbsenceTimeoutMin {
		d = ownerAbsenceTimeoutMin
	}
	if d > ownerAbsenceTimeoutMax {
		d = ownerAbsenceTimeoutMax
	}
	return d.Milliseconds()
}

func (s *Service) getRequiredSessionTx(ctx context.Context, q *repo.Queries, id string) (repo.Session, error) {
	row, err := q.GetSessionByID(ctx, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Session{}, fmt.Errorf("%w: %s", ErrSessionNotFound, id)
		}
		return repo.Session{}, fmt.Errorf("get session: %w", err)
	}
	return row, nil
}

func (s *Service) getRequiredSession(ctx context.Context, id string) (repo.Session, error) {
	return s.getRequiredSessionTx(ctx, s.q, id)
}

func (s *Service) getRequiredAttachmentTx(ctx context.Context, q *repo.Queries, sessionID, attachmentID string) (repo.Attachment, error) {
	row, err := q.GetAttachmentByID(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.Attachment{}, fmt.Errorf("%w: %s", ErrAttachmentNotFound, attachmentID)
		}
		return repo.Attachment{}, fmt.Errorf("get attachment: %w", err)
	}
	if row.SessionID != sessionID {
		return repo.Attachment{}, fmt.Errorf("%w: %s", ErrAttachmentNotFound, attachmentID)
	}
	return row, nil
}

func (s *Service) getRequiredAttachment(ctx context.Context, sessionID, attachmentID string) (repo.Attachment, error) {
	return s.getRequiredAttachmentTx(ctx, s.q, sessionID, attachmentID)
}

func assertSessionMutable(row repo.Session) error {
	if row.Status == "closed" {
		return fmt.Errorf("%w: session is closed: %s", ErrInvalidRequest, row.ID)
	}
	return nil
}

func (s *Service) assertAccountOwnsSessionTx(ctx context.Context, q *repo.Queries, row repo.Session, caller auth.AuthenticatedUser) error {
	if caller.Scope != "account" {
		return fmt.Errorf("%w: session moderation requires an account credential", ErrForbidden)
	}
	instance, err := q.GetInstanceByID(ctx, row.InstanceID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: caller is not the session owner: %s", ErrForbidden, row.ID)
		}
		return fmt.Errorf("get instance: %w", err)
	}
	if instance.OwnerID != caller.UserID {
		return fmt.Errorf("%w: caller is not the session owner: %s", ErrForbidden, row.ID)
	}
	return nil
}

func (s *Service) assertAccountOwnsSession(ctx context.Context, row repo.Session, caller auth.AuthenticatedUser) error {
	return s.assertAccountOwnsSessionTx(ctx, s.q, row, caller)
}

func assertAttachmentCaller(sessionID, attachmentID string, caller auth.AuthenticatedUser) error {
	if caller.Scope != "attachment" ||
		caller.SessionID == nil || *caller.SessionID != sessionID ||
		caller.AttachmentID == nil || *caller.AttachmentID != attachmentID {
		return fmt.Errorf("%w: attachment credential is not bound to this session", ErrForbidden)
	}
	return nil
}

func (s *Service) getSessionRecordTx(ctx context.Context, q *repo.Queries, sessionID string) (SessionRecord, error) {
	row, err := s.getRequiredSessionTx(ctx, q, sessionID)
	if err != nil {
		return SessionRecord{}, err
	}
	attachments, err := q.ListAttachmentsForSession(ctx, sessionID)
	if err != nil {
		return SessionRecord{}, fmt.Errorf("list attachments for session: %w", err)
	}
	return toSessionRecord(row, attachments), nil
}

// GetSessionUnchecked skips caller authorization — for internal, non-HTTP
// callers (transport package) that already authorized the request at a
// higher layer.
func (s *Service) GetSessionUnchecked(ctx context.Context, sessionID string) (SessionRecord, error) {
	return s.getSessionRecordTx(ctx, s.q, sessionID)
}

// CreateSession creates one active session for instanceID plus an "owner"
// attachment bound to caller, and issues that attachment's first
// credential — all in one transaction, so a half-created session can never
// be observed. ownerAbsenceTimeoutMs, if given, is clamped to
// [ownerAbsenceTimeoutMin, ownerAbsenceTimeoutMax].
func (s *Service) CreateSession(
	ctx context.Context,
	instanceID, ownerClientID string,
	ownerAbsenceTimeoutMs *int64,
	caller auth.AuthenticatedUser,
) (SessionRecord, string, error) {
	if caller.Scope != "account" {
		return SessionRecord{}, "", fmt.Errorf("%w: session creation requires an account credential", ErrForbidden)
	}

	var clampedTimeout sql.NullInt64
	if ownerAbsenceTimeoutMs != nil {
		clampedTimeout = sql.NullInt64{Int64: clampOwnerAbsenceTimeoutMs(*ownerAbsenceTimeoutMs), Valid: true}
	}

	sessionID := uuid.NewString()
	attachmentID := uuid.NewString()
	var result SessionRecord
	var token string

	err := s.withTx(ctx, func(q *repo.Queries) error {
		instance, err := q.GetInstanceByID(ctx, instanceID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: instance not found: %s", ErrSessionNotFound, instanceID)
			}
			return fmt.Errorf("get instance: %w", err)
		}
		if instance.OwnerID != caller.UserID {
			return fmt.Errorf("%w: caller does not own instance %s", ErrForbidden, instanceID)
		}

		existing, err := q.ListSessionsForInstance(ctx, instanceID)
		if err != nil {
			return fmt.Errorf("list sessions for instance: %w", err)
		}
		for _, sess := range existing {
			if sess.Status != "closed" {
				return fmt.Errorf("%w: active session already exists for instance %s", ErrSessionConflict, instanceID)
			}
		}

		if _, err := q.CreateSession(ctx, repo.CreateSessionParams{
			ID:                    sessionID,
			InstanceID:            instanceID,
			OwnerAbsenceTimeoutMs: clampedTimeout,
		}); err != nil {
			return fmt.Errorf("create session: %w", err)
		}
		if _, err := q.CreateAttachment(ctx, repo.CreateAttachmentParams{
			ID:        attachmentID,
			SessionID: sessionID,
			ClientID:  ownerClientID,
			UserID:    sql.NullString{String: caller.UserID, Valid: true},
			Role:      "owner",
			Status:    "connected",
		}); err != nil {
			return fmt.Errorf("create owner attachment: %w", err)
		}

		expiresAt := s.now().Add(attachmentCredentialTTL)
		tok, err := s.authSvc.IssueAttachmentCredentialTx(ctx, q, caller.UserID, sessionID, attachmentID, expiresAt)
		if err != nil {
			return fmt.Errorf("issue attachment credential: %w", err)
		}
		token = tok

		record, err := s.getSessionRecordTx(ctx, q, sessionID)
		if err != nil {
			return err
		}
		result = record
		return nil
	})
	if err != nil {
		return SessionRecord{}, "", err
	}
	return result, token, nil
}

// ConnectAttachment marks attachmentID "connected". No caller-authorization
// check here, matching session-service.ts's connectAttachment: the worker
// signaling handshake is itself gated by possession of the attachment's
// credential, authorized upstream of this call.
func (s *Service) ConnectAttachment(ctx context.Context, sessionID, attachmentID string) (AttachmentRecord, error) {
	row, err := s.getRequiredSession(ctx, sessionID)
	if err != nil {
		return AttachmentRecord{}, err
	}
	if err := assertSessionMutable(row); err != nil {
		return AttachmentRecord{}, err
	}
	attachment, err := s.getRequiredAttachment(ctx, sessionID, attachmentID)
	if err != nil {
		return AttachmentRecord{}, err
	}
	if attachment.Status != "connected" {
		if err := s.q.UpdateAttachmentStatus(ctx, repo.UpdateAttachmentStatusParams{Status: "connected", ID: attachment.ID}); err != nil {
			return AttachmentRecord{}, fmt.Errorf("update attachment status: %w", err)
		}
		if attachment, err = s.getRequiredAttachment(ctx, sessionID, attachmentID); err != nil {
			return AttachmentRecord{}, err
		}
	}
	return toAttachmentRecord(attachment), nil
}

// DisconnectAttachment is KeyedMutex-serialized per attachment: it is the
// DB-only half of a disconnect — the caller (route layer, via
// transport.Service) is responsible for the worker-side teardown
// afterward, using the same AttachmentKey so the two stay ordered relative
// to a concurrent resume/role-transition on the same attachment.
func (s *Service) DisconnectAttachment(
	ctx context.Context,
	sessionID, attachmentID string,
	caller auth.AuthenticatedUser,
	actor audit.Context,
) (SessionRecord, error) {
	unlock := s.keyedMutex.Lock(AttachmentKey(sessionID, attachmentID))
	defer unlock()

	var result SessionRecord
	err := s.withTx(ctx, func(q *repo.Queries) error {
		row, err := s.getRequiredSessionTx(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if err := assertSessionMutable(row); err != nil {
			return err
		}
		attachment, err := s.getRequiredAttachmentTx(ctx, q, sessionID, attachmentID)
		if err != nil {
			return err
		}

		kickedByAccount := caller.Scope == "account"
		if caller.Scope == "attachment" {
			if err := assertAttachmentCaller(sessionID, attachmentID, caller); err != nil {
				return err
			}
		} else if err := s.assertAccountOwnsSessionTx(ctx, q, row, caller); err != nil {
			return err
		}

		wasAlreadyDisconnected := attachment.Status == "disconnected"
		if !wasAlreadyDisconnected {
			if err := q.UpdateAttachmentStatus(ctx, repo.UpdateAttachmentStatusParams{Status: "disconnected", ID: attachment.ID}); err != nil {
				return fmt.Errorf("update attachment status: %w", err)
			}
		}
		if kickedByAccount {
			if err := q.RevokeAttachmentCredentials(ctx, repo.RevokeAttachmentCredentialsParams{
				SessionID:    sql.NullString{String: sessionID, Valid: true},
				AttachmentID: sql.NullString{String: attachment.ID, Valid: true},
			}); err != nil {
				return fmt.Errorf("revoke attachment credentials: %w", err)
			}
		}
		if err := q.SetAttachmentTransportTeardownPending(ctx, repo.SetAttachmentTransportTeardownPendingParams{
			TransportTeardownPending: 1,
			ID:                       attachment.ID,
		}); err != nil {
			return fmt.Errorf("set transport teardown pending: %w", err)
		}
		if attachment.Role == "owner" && !wasAlreadyDisconnected && !row.OwnerAbsenceDeadline.Valid {
			timeout := s.defaultOwnerAbsenceTimeout
			if row.OwnerAbsenceTimeoutMs.Valid {
				timeout = time.Duration(row.OwnerAbsenceTimeoutMs.Int64) * time.Millisecond
			}
			deadline := formatTimestamp(s.now().Add(timeout))
			if err := q.SetOwnerAbsenceDeadline(ctx, repo.SetOwnerAbsenceDeadlineParams{
				OwnerAbsenceDeadline: sql.NullString{String: deadline, Valid: true},
				ID:                   sessionID,
			}); err != nil {
				return fmt.Errorf("set owner absence deadline: %w", err)
			}
		}
		if kickedByAccount {
			if err := audit.Record(ctx, q, actor, actionSessionAttachmentDisconnect, targetTypeAttachment, attachment.ID, audit.OutcomeSuccess); err != nil {
				return err
			}
		}

		record, err := s.getSessionRecordTx(ctx, q, sessionID)
		if err != nil {
			return err
		}
		result = record
		return nil
	})
	if err != nil {
		return SessionRecord{}, err
	}
	return result, nil
}

// ResumeAttachment is KeyedMutex-serialized per attachment (see
// DisconnectAttachment): it rotates the caller's attachment credential and
// marks the attachment connected again, clearing any owner-absence
// deadline if this is the owner attachment reconnecting.
func (s *Service) ResumeAttachment(
	ctx context.Context,
	sessionID, attachmentID string,
	caller auth.AuthenticatedUser,
) (AttachmentRecord, string, error) {
	unlock := s.keyedMutex.Lock(AttachmentKey(sessionID, attachmentID))
	defer unlock()

	var record AttachmentRecord
	var token string
	err := s.withTx(ctx, func(q *repo.Queries) error {
		row, err := s.getRequiredSessionTx(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if err := assertSessionMutable(row); err != nil {
			return err
		}
		if err := assertAttachmentCaller(sessionID, attachmentID, caller); err != nil {
			return err
		}
		attachment, err := s.getRequiredAttachmentTx(ctx, q, sessionID, attachmentID)
		if err != nil {
			return err
		}

		tok, err := s.authSvc.RotateAttachmentCredentialTx(ctx, q, caller.CredentialID, caller.UserID, sessionID, attachmentID)
		if err != nil {
			return fmt.Errorf("rotate attachment credential: %w", err)
		}
		token = tok

		if attachment.Status != "connected" {
			if err := q.UpdateAttachmentStatus(ctx, repo.UpdateAttachmentStatusParams{Status: "connected", ID: attachment.ID}); err != nil {
				return fmt.Errorf("update attachment status: %w", err)
			}
		}
		if attachment.Role == "owner" {
			if err := q.ClearOwnerAbsenceDeadline(ctx, sessionID); err != nil {
				return fmt.Errorf("clear owner absence deadline: %w", err)
			}
		}

		updated, err := q.GetAttachmentByID(ctx, attachment.ID)
		if err != nil {
			return fmt.Errorf("reload attachment: %w", err)
		}
		record = toAttachmentRecord(updated)
		return nil
	})
	if err != nil {
		return AttachmentRecord{}, "", err
	}
	return record, token, nil
}

// RefreshAttachmentCredential rotates the caller's attachment credential
// without touching connection status.
func (s *Service) RefreshAttachmentCredential(
	ctx context.Context,
	sessionID, attachmentID string,
	caller auth.AuthenticatedUser,
) (string, error) {
	var token string
	err := s.withTx(ctx, func(q *repo.Queries) error {
		row, err := s.getRequiredSessionTx(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if err := assertSessionMutable(row); err != nil {
			return err
		}
		if err := assertAttachmentCaller(sessionID, attachmentID, caller); err != nil {
			return err
		}
		if _, err := s.getRequiredAttachmentTx(ctx, q, sessionID, attachmentID); err != nil {
			return err
		}
		tok, err := s.authSvc.RotateAttachmentCredentialTx(ctx, q, caller.CredentialID, caller.UserID, sessionID, attachmentID)
		if err != nil {
			return fmt.Errorf("rotate attachment credential: %w", err)
		}
		token = tok
		return nil
	})
	if err != nil {
		return "", err
	}
	return token, nil
}

// GetSession authorizes caller (account owner, or the bound attachment
// credential) before returning sessionID's record.
func (s *Service) GetSession(ctx context.Context, sessionID string, caller auth.AuthenticatedUser) (SessionRecord, error) {
	row, err := s.getRequiredSession(ctx, sessionID)
	if err != nil {
		return SessionRecord{}, err
	}
	if caller.Scope == "attachment" {
		if caller.SessionID == nil || *caller.SessionID != sessionID || caller.AttachmentID == nil {
			return SessionRecord{}, fmt.Errorf("%w: attachment credential is not bound to this session", ErrForbidden)
		}
		if _, err := s.getRequiredAttachment(ctx, sessionID, *caller.AttachmentID); err != nil {
			return SessionRecord{}, err
		}
	} else if err := s.assertAccountOwnsSession(ctx, row, caller); err != nil {
		return SessionRecord{}, err
	}
	return s.getSessionRecordTx(ctx, s.q, sessionID)
}

// AuthorizeSessionModeration reports whether caller may moderate
// (promote/demote/close) sessionID — exported for transport.Service's
// TransitionAttachmentRole, which needs the same check before touching the
// worker.
func (s *Service) AuthorizeSessionModeration(ctx context.Context, sessionID string, caller auth.AuthenticatedUser) error {
	row, err := s.getRequiredSession(ctx, sessionID)
	if err != nil {
		return err
	}
	return s.assertAccountOwnsSession(ctx, row, caller)
}

// CompleteAttachmentTransportTeardown clears attachmentID's
// transport-teardown-pending flag once the caller has confirmed the worker
// side is torn down.
func (s *Service) CompleteAttachmentTransportTeardown(ctx context.Context, sessionID, attachmentID string) error {
	if _, err := s.getRequiredAttachment(ctx, sessionID, attachmentID); err != nil {
		return err
	}
	if err := s.q.SetAttachmentTransportTeardownPending(ctx, repo.SetAttachmentTransportTeardownPendingParams{
		TransportTeardownPending: 0,
		ID:                       attachmentID,
	}); err != nil {
		return fmt.Errorf("set transport teardown pending: %w", err)
	}
	return nil
}

// closeSessionTx is the shared core of CloseSession/CloseSessionTx: it
// disconnects every attachment, revokes their credentials, marks them all
// transport-teardown-pending, and closes the session, all against the
// caller-supplied q. Unlike session-service.ts's closeSession, it does not
// take an injected destroyAttachmentTransport callback and does not attempt
// the worker-side teardown itself (that TS version silently swallowed
// per-attachment teardown failures inside a try/catch). Instead it returns
// the closed record plus the IDs that still need worker teardown; the
// caller drives that loop (transport.Service.TeardownPendingAttachment) once
// its own transaction has committed, so failures stay visible instead of
// being swallowed.
func (s *Service) closeSessionTx(ctx context.Context, q *repo.Queries, sessionID string, actor audit.Context) (SessionRecord, []string, error) {
	if _, err := s.getRequiredSessionTx(ctx, q, sessionID); err != nil {
		return SessionRecord{}, nil, err
	}
	attachments, err := q.ListAttachmentsForSession(ctx, sessionID)
	if err != nil {
		return SessionRecord{}, nil, fmt.Errorf("list attachments for session: %w", err)
	}
	var attachmentIDs []string
	for _, a := range attachments {
		if a.Status != "disconnected" {
			if err := q.UpdateAttachmentStatus(ctx, repo.UpdateAttachmentStatusParams{Status: "disconnected", ID: a.ID}); err != nil {
				return SessionRecord{}, nil, fmt.Errorf("update attachment status: %w", err)
			}
		}
		if err := q.RevokeAttachmentCredentials(ctx, repo.RevokeAttachmentCredentialsParams{
			SessionID:    sql.NullString{String: sessionID, Valid: true},
			AttachmentID: sql.NullString{String: a.ID, Valid: true},
		}); err != nil {
			return SessionRecord{}, nil, fmt.Errorf("revoke attachment credentials: %w", err)
		}
		if err := q.SetAttachmentTransportTeardownPending(ctx, repo.SetAttachmentTransportTeardownPendingParams{
			TransportTeardownPending: 1,
			ID:                       a.ID,
		}); err != nil {
			return SessionRecord{}, nil, fmt.Errorf("set transport teardown pending: %w", err)
		}
		attachmentIDs = append(attachmentIDs, a.ID)
	}
	if err := q.CloseSession(ctx, sessionID); err != nil {
		return SessionRecord{}, nil, fmt.Errorf("close session: %w", err)
	}
	if err := audit.Record(ctx, q, actor, actionSessionClose, targetTypeSession, sessionID, audit.OutcomeSuccess); err != nil {
		return SessionRecord{}, nil, err
	}
	record, err := s.getSessionRecordTx(ctx, q, sessionID)
	if err != nil {
		return SessionRecord{}, nil, err
	}
	return record, attachmentIDs, nil
}

// CloseSession closes sessionID unconditionally, in its own transaction.
func (s *Service) CloseSession(ctx context.Context, sessionID string, actor audit.Context) (SessionRecord, []string, error) {
	var result SessionRecord
	var attachmentIDs []string
	err := s.withTx(ctx, func(q *repo.Queries) error {
		record, ids, err := s.closeSessionTx(ctx, q, sessionID, actor)
		result, attachmentIDs = record, ids
		return err
	})
	if err != nil {
		return SessionRecord{}, nil, err
	}
	return result, attachmentIDs, nil
}

// CloseSessionTx is CloseSession against an explicit q, for callers (the
// admin package's ban flow) that need the close to commit atomically with
// other work in their own transaction — same pattern as
// auth.Service.RequireActiveAdmin taking an external q.
func (s *Service) CloseSessionTx(ctx context.Context, q *repo.Queries, sessionID string, actor audit.Context) (SessionRecord, []string, error) {
	return s.closeSessionTx(ctx, q, sessionID, actor)
}

// CloseExpiredOwnerAbsenceSession closes sessionID only if, as of now, it is
// still active with an owner-absence deadline that has passed — checked and
// acted on inside one transaction, so a concurrent resume (which clears the
// deadline) can't race it. closed is false, with no error, whenever the
// session is no longer eligible (already closed, deadline cleared, or
// deadline not yet reached) — the caller, the owner-absence Reconciler, can
// call this repeatedly and safely treat it as a no-op in that case.
func (s *Service) CloseExpiredOwnerAbsenceSession(ctx context.Context, sessionID string, now time.Time, actor audit.Context) (record SessionRecord, attachmentIDs []string, closed bool, err error) {
	nowStr := formatTimestamp(now)
	err = s.withTx(ctx, func(q *repo.Queries) error {
		sess, txErr := s.getRequiredSessionTx(ctx, q, sessionID)
		if txErr != nil {
			if errors.Is(txErr, ErrSessionNotFound) {
				return nil
			}
			return txErr
		}
		if sess.Status != "active" || !sess.OwnerAbsenceDeadline.Valid || sess.OwnerAbsenceDeadline.String > nowStr {
			return nil
		}
		r, ids, txErr := s.closeSessionTx(ctx, q, sessionID, actor)
		if txErr != nil {
			return txErr
		}
		record, attachmentIDs, closed = r, ids, true
		return nil
	})
	if err != nil {
		return SessionRecord{}, nil, false, err
	}
	return record, attachmentIDs, closed, nil
}

// ListExpiredOwnerAbsenceSessionIDs returns the IDs of active sessions whose
// owner-absence deadline has passed as of now. This is candidate discovery
// for the owner-absence Reconciler's periodic resync only — Reconcile itself
// always re-verifies via CloseExpiredOwnerAbsenceSession, so this list is
// never required to be race-free against a concurrent resume.
func (s *Service) ListExpiredOwnerAbsenceSessionIDs(ctx context.Context, now time.Time) ([]string, error) {
	rows, err := s.q.ListSessionsWithExpiredOwnerAbsence(ctx, sql.NullString{String: formatTimestamp(now), Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list sessions with expired owner absence: %w", err)
	}
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids, nil
}

// SetAttachmentPendingTargetRole and ConfirmAttachmentRole are the two
// low-level, transaction-participating primitives
// transport.Service.TransitionAttachmentRole composes into the
// mark-pending / call-worker / confirm-or-rollback sequence described in
// docs/plans/session-transport-redesign.md. Neither does its own
// authorization or audit recording — the caller owns both, alongside its
// own transaction and worker call, via q.

// SetAttachmentPendingTargetRole sets (or, with targetRole == nil, clears)
// attachmentID's pending_target_role.
func (s *Service) SetAttachmentPendingTargetRole(
	ctx context.Context,
	q *repo.Queries,
	sessionID, attachmentID string,
	targetRole *string,
) (AttachmentRecord, error) {
	attachment, err := s.getRequiredAttachmentTx(ctx, q, sessionID, attachmentID)
	if err != nil {
		return AttachmentRecord{}, err
	}
	if err := q.SetAttachmentPendingTargetRole(ctx, repo.SetAttachmentPendingTargetRoleParams{
		PendingTargetRole: nullStringFromPtr(targetRole),
		ID:                attachment.ID,
	}); err != nil {
		return AttachmentRecord{}, fmt.Errorf("set attachment pending target role: %w", err)
	}
	updated, err := q.GetAttachmentByID(ctx, attachment.ID)
	if err != nil {
		return AttachmentRecord{}, fmt.Errorf("reload attachment: %w", err)
	}
	return toAttachmentRecord(updated), nil
}

// ConfirmAttachmentRole sets attachmentID's confirmed role and clears any
// pending_target_role in one update — the success path of a role
// transition, once the worker has confirmed it is enforcing role.
func (s *Service) ConfirmAttachmentRole(
	ctx context.Context,
	q *repo.Queries,
	sessionID, attachmentID, role string,
) (AttachmentRecord, error) {
	attachment, err := s.getRequiredAttachmentTx(ctx, q, sessionID, attachmentID)
	if err != nil {
		return AttachmentRecord{}, err
	}
	if err := q.UpdateAttachmentRole(ctx, repo.UpdateAttachmentRoleParams{Role: role, ID: attachment.ID}); err != nil {
		return AttachmentRecord{}, fmt.Errorf("update attachment role: %w", err)
	}
	if err := q.SetAttachmentPendingTargetRole(ctx, repo.SetAttachmentPendingTargetRoleParams{ID: attachment.ID}); err != nil {
		return AttachmentRecord{}, fmt.Errorf("clear attachment pending target role: %w", err)
	}
	updated, err := q.GetAttachmentByID(ctx, attachment.ID)
	if err != nil {
		return AttachmentRecord{}, fmt.Errorf("reload attachment: %w", err)
	}
	return toAttachmentRecord(updated), nil
}
