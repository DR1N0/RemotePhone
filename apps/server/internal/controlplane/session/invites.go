package session

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/persistence/repo"

	"github.com/google/uuid"
)

const (
	actionSessionInviteIssue  = "session.invite.issue"
	actionSessionInviteRedeem = "session.invite.redeem"
	targetTypeSessionInvite   = "session_invite"
)

// sessionInviteTTL is fixed, not caller-configurable: a 6-digit PIN has far
// less entropy than this package's other bearer tokens (1,000,000 possible
// values vs. 2^256), so unlike the old long-opaque-token invite design, the
// validity window is part of what keeps redemption safe, not just a UX
// knob. 5 minutes matches "read the code to a friend and they join right
// now" — not a link to forward later.
const sessionInviteTTL = 5 * time.Minute

const maxGuestDisplayNameLen = 40

// generateSessionInvitePIN returns a 6-digit numeric PIN, zero-padded
// ("004213"), drawn from crypto/rand. Short and spoken/typed easily,
// matching the Zoom/Meet/WiFi-share convention this is modeled on —
// nothing in this package's invite flow needs the 256-bit entropy
// auth.GenerateToken provides for bearer credentials.
func generateSessionInvitePIN() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", fmt.Errorf("generate session invite pin: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

// validateGuestDisplayName lives here ONLY — session-invite-service.ts and
// routes/invites.ts both implemented this identical check, once as the
// service's real validation and once, redundantly, as a route-layer
// pre-check. RedeemSessionInvite is the single call site; the route layer
// (task #26) passes displayName through unvalidated and maps
// ErrInvalidRequest to 400 like every other service error.
func validateGuestDisplayName(displayName *string) (string, error) {
	if displayName == nil {
		return "Guest", nil
	}
	trimmed := strings.TrimSpace(*displayName)
	if len(*displayName) < 1 || len(*displayName) > maxGuestDisplayNameLen || trimmed == "" {
		return "", fmt.Errorf("%w: guest displayName must be 1 to %d non-whitespace characters", ErrInvalidRequest, maxGuestDisplayNameLen)
	}
	return *displayName, nil
}

// CreateSessionInvite issues a 6-digit PIN valid for sessionInviteTTL,
// letting anyone who has it join sessionID as an observer (see
// RedeemSessionInvite) without needing an account. Only the session's owner
// may create one.
func (s *Service) CreateSessionInvite(
	ctx context.Context,
	sessionID string,
	caller auth.AuthenticatedUser,
	actor audit.Context,
) (pin string, expiresAt string, err error) {
	pin, err = generateSessionInvitePIN()
	if err != nil {
		return "", "", err
	}
	expiry := s.now().Add(sessionInviteTTL)
	inviteID := uuid.NewString()

	err = s.withTx(ctx, func(q *repo.Queries) error {
		row, err := s.getRequiredSessionTx(ctx, q, sessionID)
		if err != nil {
			return err
		}
		if err := assertSessionMutable(row); err != nil {
			return err
		}
		if err := s.assertAccountOwnsSessionTx(ctx, q, row, caller); err != nil {
			return err
		}

		// PIN hashes are unsalted SHA-256 (auth.HashToken), same as every
		// other credential in this schema — but unlike a 256-bit bearer
		// token, a 6-digit PIN's whole hash space is cheaply precomputable.
		// That only matters to someone who already has read access to the
		// database file, at which point far worse things than a 5-minute
		// invite PIN are already exposed; not worth a peppered-hash scheme
		// for this project's threat model.
		if _, err := q.CreateSessionInvite(ctx, repo.CreateSessionInviteParams{
			ID:              inviteID,
			SessionID:       sessionID,
			CreatedByUserID: caller.UserID,
			TokenHash:       auth.HashToken(pin),
			ExpiresAt:       formatTimestamp(expiry),
		}); err != nil {
			return fmt.Errorf("create session invite: %w", err)
		}
		return audit.Record(ctx, q, actor, actionSessionInviteIssue, targetTypeSessionInvite, inviteID, audit.OutcomeSuccess)
	})
	if err != nil {
		return "", "", err
	}
	return pin, formatTimestamp(expiry), nil
}

const (
	SessionInviteModeRegistered = "registered"
	SessionInviteModeGuest      = "guest"
)

// RedeemSessionInvite consumes pin exactly once and attaches the resulting
// caller to the invite's session as a new "observer" attachment. mode
// "registered" binds the new attachment to requesterUserID (an existing,
// active, non-guest account); mode "guest" creates a fresh lightweight
// guest user from displayName (validated here — see validateGuestDisplayName
// — the route layer does not re-validate). actorCredentialID is nil for
// guest redemptions, which have no credential yet.
func (s *Service) RedeemSessionInvite(
	ctx context.Context,
	pin, mode string,
	requesterUserID *string,
	displayName *string,
	actorCredentialID *string,
	requestID string,
) (AttachmentRecord, string, auth.UserDTO, error) {
	var guestDisplayName string
	if mode == SessionInviteModeGuest {
		name, err := validateGuestDisplayName(displayName)
		if err != nil {
			return AttachmentRecord{}, "", auth.UserDTO{}, err
		}
		guestDisplayName = name
	} else if mode != SessionInviteModeRegistered {
		return AttachmentRecord{}, "", auth.UserDTO{}, fmt.Errorf("%w: mode must be %q or %q", ErrInvalidRequest, SessionInviteModeRegistered, SessionInviteModeGuest)
	}

	var record AttachmentRecord
	var token string
	var userDTO auth.UserDTO
	err := s.withTx(ctx, func(q *repo.Queries) error {
		redeemedAt := s.now()
		invite, err := q.GetSessionInviteByTokenHash(ctx, auth.HashToken(pin))
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w", ErrSessionInviteInvalid)
			}
			return fmt.Errorf("get session invite: %w", err)
		}
		rowsAffected, err := q.ConsumeSessionInvite(ctx, repo.ConsumeSessionInviteParams{
			ConsumedAt: sql.NullString{String: formatTimestamp(redeemedAt), Valid: true},
			ID:         invite.ID,
			ExpiresAt:  formatTimestamp(redeemedAt),
		})
		if err != nil {
			return fmt.Errorf("consume session invite: %w", err)
		}
		if rowsAffected == 0 {
			return fmt.Errorf("%w", ErrSessionInviteInvalid)
		}

		session, err := q.GetSessionByID(ctx, invite.SessionID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w", ErrSessionInviteInvalid)
			}
			return fmt.Errorf("get session: %w", err)
		}
		if session.Status != "active" {
			return fmt.Errorf("%w", ErrSessionInviteInvalid)
		}

		var userID string
		if mode == SessionInviteModeGuest {
			userID = uuid.NewString()
			if _, err := q.CreateUser(ctx, repo.CreateUserParams{
				ID:          userID,
				DisplayName: guestDisplayName,
				UserType:    "guest",
				Status:      "active",
			}); err != nil {
				return fmt.Errorf("create guest user: %w", err)
			}
		} else {
			if requesterUserID == nil {
				return fmt.Errorf("%w: registered redemption requires an account user", ErrSessionInviteInvalid)
			}
			user, err := q.GetUserByID(ctx, *requesterUserID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: registered redemption requires an active registered user", ErrSessionInviteInvalid)
				}
				return fmt.Errorf("get user: %w", err)
			}
			if user.Status != "active" || user.UserType == "guest" {
				return fmt.Errorf("%w: registered redemption requires an active registered user", ErrSessionInviteInvalid)
			}
			userID = user.ID
		}

		attachmentID := uuid.NewString()
		if _, err := q.CreateAttachment(ctx, repo.CreateAttachmentParams{
			ID:        attachmentID,
			SessionID: invite.SessionID,
			ClientID:  "invite-" + attachmentID,
			UserID:    sql.NullString{String: userID, Valid: true},
			Role:      "observer",
			Status:    "connected",
		}); err != nil {
			return fmt.Errorf("create observer attachment: %w", err)
		}

		tok, err := s.authSvc.IssueAttachmentCredentialTx(ctx, q, userID, invite.SessionID, attachmentID, redeemedAt.Add(attachmentCredentialTTL))
		if err != nil {
			return fmt.Errorf("issue attachment credential: %w", err)
		}
		token = tok

		user, err := s.authSvc.GetUserTx(ctx, q, userID)
		if err != nil {
			return fmt.Errorf("get redeemed invite user: %w", err)
		}
		userDTO = user

		attachment, err := q.GetAttachmentByID(ctx, attachmentID)
		if err != nil {
			return fmt.Errorf("reload attachment: %w", err)
		}
		record = toAttachmentRecord(attachment)

		actor := audit.NewContext(&userID, actorCredentialID, requestID)
		return audit.Record(ctx, q, actor, actionSessionInviteRedeem, targetTypeSessionInvite, invite.ID, audit.OutcomeSuccess)
	})
	if err != nil {
		return AttachmentRecord{}, "", auth.UserDTO{}, err
	}
	return record, token, userDTO, nil
}
