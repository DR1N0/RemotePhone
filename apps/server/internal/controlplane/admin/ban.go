package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/persistence/repo"
)

// RemoveUser soft-deletes an account: it revokes/closes all access, clears the
// username and password so the name can be reused, and leaves the row for
// audit/foreign-key integrity. It refuses to remove _SYSTEM or the final
// active admin.
func (s *Service) RemoveUser(ctx context.Context, targetUserID, callerUserID string, actor audit.Context) ([]TeardownTarget, error) {
	if _, err := s.authSvc.RequireActiveAdmin(ctx, s.q, callerUserID); err != nil {
		return nil, err
	}
	target, err := requireTarget(ctx, s.q, targetUserID)
	if err != nil {
		return nil, err
	}
	if target.ID == "_SYSTEM" {
		return nil, ErrCannotRemoveSystem
	}
	if err := s.removeOwnedInstances(ctx, target.ID, callerUserID, actor); err != nil {
		return nil, err
	}

	var teardownTargets []TeardownTarget
	err = s.withTx(ctx, func(q *repo.Queries) error {
		if _, err := s.authSvc.RequireActiveAdmin(ctx, q, callerUserID); err != nil {
			return err
		}
		target, err := requireTarget(ctx, q, targetUserID)
		if err != nil {
			return err
		}
		if target.ID == "_SYSTEM" {
			return ErrCannotRemoveSystem
		}
		if err := requireNotFinalActiveAdmin(ctx, q, target, "registered", "banned"); err != nil &&
			callerUserID != auth.SystemUserID {
			return err
		}
		targets, err := s.closeBannedUserAccessTx(ctx, q, target.ID, actor)
		if err != nil {
			return err
		}
		if err := q.SoftDeleteUser(ctx, target.ID); err != nil {
			return fmt.Errorf("soft delete user: %w", err)
		}
		teardownTargets = targets
		return audit.Record(ctx, q, actor, "user.remove", "user", target.ID, audit.OutcomeSuccess)
	})
	if err != nil {
		return nil, err
	}
	return teardownTargets, nil
}

// removeOwnedInstances stops and deletes every instance owned by targetUserID
// before the account tombstone is written. Instance stop/delete is
// intentionally outside the account transaction because it may call workers
// and clean up AVD data.
func (s *Service) removeOwnedInstances(ctx context.Context, targetUserID, callerUserID string, actor audit.Context) error {
	instances, err := s.instanceSvc.ListAllInstances(ctx)
	if err != nil {
		return fmt.Errorf("list owned instances: %w", err)
	}
	for _, managed := range instances {
		if managed.OwnerID != targetUserID {
			continue
		}
		if _, err := s.instanceSvc.StopAdminManagedInstance(ctx, managed.ID, callerUserID, actor); err != nil {
			return fmt.Errorf("stop instance %s for account removal: %w", managed.ID, err)
		}
		if err := s.instanceSvc.DeleteAdminManagedInstance(ctx, managed.ID, callerUserID, actor); err != nil {
			return fmt.Errorf("delete instance %s for account removal: %w", managed.ID, err)
		}
	}
	return nil
}

// SetBanStatus sets targetUserID's status (refusing to ban the final active
// admin, via requireNotFinalActiveAdmin). Banning closes every session
// targetUserID owns (via sessionSvc.CloseSessionTx, atomically with the ban
// itself) and revokes targetUserID's attachments on every other session it
// merely participates in. Returns every attachment that still needs
// worker-side transport teardown; the caller drives that afterward.
func (s *Service) SetBanStatus(ctx context.Context, targetUserID, status, callerUserID string, actor audit.Context) ([]TeardownTarget, error) {
	var teardownTargets []TeardownTarget
	err := s.withTx(ctx, func(q *repo.Queries) error {
		if _, err := s.authSvc.RequireActiveAdmin(ctx, q, callerUserID); err != nil {
			return err
		}
		target, err := requireTarget(ctx, q, targetUserID)
		if err != nil {
			return err
		}
		if err := requireNotFinalActiveAdmin(ctx, q, target, target.UserType, status); err != nil {
			return err
		}
		if err := q.SetUserStatus(ctx, repo.SetUserStatusParams{Status: status, ID: target.ID}); err != nil {
			return fmt.Errorf("set user status: %w", err)
		}
		if status != "banned" {
			return audit.Record(ctx, q, actor, actionUserUnban, "user", target.ID, audit.OutcomeSuccess)
		}
		targets, err := s.closeBannedUserAccessTx(ctx, q, target.ID, actor)
		if err != nil {
			return err
		}
		teardownTargets = targets
		return audit.Record(ctx, q, actor, actionUserBan, "user", target.ID, audit.OutcomeSuccess)
	})
	if err != nil {
		return nil, err
	}
	return teardownTargets, nil
}

// closeBannedUserAccessTx tears down every session userID can reach: owned
// sessions are closed outright (sessionSvc.CloseSessionTx disconnects every
// attachment on them, not just userID's own), everything else is a single
// attachment revoked in place via revokeAttachmentAccessTx.
func (s *Service) closeBannedUserAccessTx(ctx context.Context, q *repo.Queries, userID string, actor audit.Context) ([]TeardownTarget, error) {
	affected, err := q.ListAttachmentsForUser(ctx, sql.NullString{String: userID, Valid: true})
	if err != nil {
		return nil, fmt.Errorf("list attachments for user: %w", err)
	}
	ownerSessionIDs := map[string]bool{}
	for _, a := range affected {
		if a.Role == "owner" {
			ownerSessionIDs[a.SessionID] = true
		}
	}

	var targets []TeardownTarget
	for sessionID := range ownerSessionIDs {
		_, attachmentIDs, err := s.sessionSvc.CloseSessionTx(ctx, q, sessionID, actor)
		if err != nil {
			return nil, err
		}
		for _, id := range attachmentIDs {
			targets = append(targets, TeardownTarget{SessionID: sessionID, AttachmentID: id})
		}
	}
	for _, a := range affected {
		if ownerSessionIDs[a.SessionID] {
			continue
		}
		if err := revokeAttachmentAccessTx(ctx, q, a.SessionID, a.ID); err != nil {
			return nil, err
		}
		targets = append(targets, TeardownTarget{SessionID: a.SessionID, AttachmentID: a.ID})
	}
	return targets, nil
}

// revokeAttachmentAccessTx disconnects attachmentID (if not already),
// revokes its credentials, and marks it transport-teardown-pending. Shared
// by the ban flow's non-owner path and RevokeGuestAttachment.
func revokeAttachmentAccessTx(ctx context.Context, q *repo.Queries, sessionID, attachmentID string) error {
	attachment, err := q.GetAttachmentByID(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrGuestAttachmentNotFound, attachmentID)
		}
		return fmt.Errorf("get attachment: %w", err)
	}
	if attachment.SessionID != sessionID {
		return fmt.Errorf("%w: %s", ErrGuestAttachmentNotFound, attachmentID)
	}
	if attachment.Status != "disconnected" {
		if err := q.UpdateAttachmentStatus(ctx, repo.UpdateAttachmentStatusParams{Status: "disconnected", ID: attachment.ID}); err != nil {
			return fmt.Errorf("update attachment status: %w", err)
		}
	}
	if err := q.RevokeAttachmentCredentials(ctx, repo.RevokeAttachmentCredentialsParams{
		SessionID:    sql.NullString{String: sessionID, Valid: true},
		AttachmentID: sql.NullString{String: attachmentID, Valid: true},
	}); err != nil {
		return fmt.Errorf("revoke attachment credentials: %w", err)
	}
	return q.SetAttachmentTransportTeardownPending(ctx, repo.SetAttachmentTransportTeardownPendingParams{
		TransportTeardownPending: 1,
		ID:                       attachmentID,
	})
}

// RevokeGuestAttachment revokes one guest's attachment to sessionID without
// closing the session or affecting any other attachment on it.
func (s *Service) RevokeGuestAttachment(ctx context.Context, sessionID, attachmentID, callerUserID string, actor audit.Context) (TeardownTarget, error) {
	var target TeardownTarget
	err := s.withTx(ctx, func(q *repo.Queries) error {
		if _, err := s.authSvc.RequireActiveAdmin(ctx, q, callerUserID); err != nil {
			return err
		}
		attachment, err := q.GetAttachmentByID(ctx, attachmentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrGuestAttachmentNotFound, attachmentID)
			}
			return fmt.Errorf("get attachment: %w", err)
		}
		if attachment.SessionID != sessionID || !attachment.UserID.Valid {
			return fmt.Errorf("%w: %s", ErrGuestAttachmentNotFound, attachmentID)
		}
		user, err := requireTarget(ctx, q, attachment.UserID.String)
		if err != nil {
			return err
		}
		if user.UserType != "guest" {
			return fmt.Errorf("%w: %s", ErrGuestAttachmentNotFound, attachmentID)
		}
		if err := revokeAttachmentAccessTx(ctx, q, sessionID, attachmentID); err != nil {
			return err
		}
		target = TeardownTarget{SessionID: sessionID, AttachmentID: attachmentID}
		return audit.Record(ctx, q, actor, actionAdminGuestRevoke, "attachment", attachment.ID, audit.OutcomeSuccess)
	})
	if err != nil {
		return TeardownTarget{}, err
	}
	return target, nil
}
