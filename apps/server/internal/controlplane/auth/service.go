// Package auth ports the TypeScript AuthService (apps/controlplane/src/services/auth-service.ts)
// to Go. Service is the central credential/identity authority: it issues and
// validates bearer tokens, and is the only place password hashes, the
// bootstrap secret, and raw tokens are ever handled together.
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"mako/internal/controlplane/persistence/repo"

	"github.com/google/uuid"
)

// accountCredentialTTL and attachmentCredentialMaxTTL mirror the TS
// constants ACCOUNT_CREDENTIAL_TTL_MS / ATTACHMENT_CREDENTIAL_MAX_TTL_MS.
const (
	accountCredentialTTL       = 30 * 24 * time.Hour
	attachmentCredentialMaxTTL = 24 * time.Hour
)

// Service is the Go port of the TS AuthService. NewService constructs it;
// Tasks 7-9 add more methods to this same type (SetupAdmin, Login,
// CreateRegistrationInvite, Register, EnrollLegacy) — do not redefine it.
type Service struct {
	db              *sql.DB
	q               *repo.Queries
	now             func() time.Time
	bootstrapSecret *BootstrapSecret
}

// AuthenticatedUser is the result of a successful Authenticate call.
// SessionID and AttachmentID are always nil for account-scoped credentials
// and always populated for attachment-scoped ones.
type AuthenticatedUser struct {
	UserID       string
	CredentialID string
	Scope        string
	SessionID    *string
	AttachmentID *string
}

// NewService constructs a Service backed by db. If bootstrapSecretPath is
// non-empty, a BootstrapSecret is created for it, and — matching the TS
// behavior exactly — if no registered/admin user exists yet, the secret
// file is created eagerly right now rather than lazily on the first setup
// request, so an operator can read it immediately after server start.
func NewService(db *sql.DB, bootstrapSecretPath string) (*Service, error) {
	svc := &Service{
		db:  db,
		q:   repo.New(db),
		now: time.Now,
	}

	if bootstrapSecretPath != "" {
		svc.bootstrapSecret = NewBootstrapSecret(bootstrapSecretPath)
		count, err := svc.q.CountRegisteredOrAdminUsers(context.Background())
		if err != nil {
			return nil, fmt.Errorf("count registered or admin users: %w", err)
		}
		if count == 0 {
			if _, err := svc.bootstrapSecret.GetOrCreate(); err != nil {
				return nil, fmt.Errorf("create bootstrap secret: %w", err)
			}
		}
	}

	return svc, nil
}

// formatTimestamp and parseTimestamp are the ONLY writer/reader of the
// not_before/expires_at TEXT columns in this task. Every write formats with
// time.RFC3339Nano after normalizing to UTC; every read parses with the same
// layout, so comparisons always go through time.Time.Before/After, never
// raw string/lexicographic comparison (see Task 6 brief's resolution note on
// the CURRENT_TIMESTAMP vs RFC3339Nano format mismatch — this task never
// compares these columns against a CURRENT_TIMESTAMP-origin value, so the
// mismatch doesn't arise here, but writer/reader format consistency still
// must hold exactly).
func formatTimestamp(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTimestamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}

func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

// GetSetupStatus reports whether the server has already been claimed (i.e.
// at least one registered or admin user exists).
func (s *Service) GetSetupStatus(ctx context.Context) (bool, error) {
	count, err := s.q.CountRegisteredOrAdminUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("count registered or admin users: %w", err)
	}
	return count > 0, nil
}

// GetUser returns the DTO for userID, or a wrapped ErrUserNotFound.
func (s *Service) GetUser(ctx context.Context, userID string) (UserDTO, error) {
	row, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserDTO{}, fmt.Errorf("%w: %s", ErrUserNotFound, userID)
		}
		return UserDTO{}, fmt.Errorf("get user: %w", err)
	}
	return toUserDTO(row), nil
}

// requireActiveUser loads userID and checks it is eligible to receive a
// credential of the given scope: not found -> ErrUserNotFound; banned ->
// ErrUserBanned; guest user requesting an "account" scoped credential ->
// ServiceError (guests ARE allowed "attachment" scoped credentials).
func (s *Service) requireActiveUser(ctx context.Context, userID, scope string) (repo.User, error) {
	user, err := s.q.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.User{}, fmt.Errorf("%w: %s", ErrUserNotFound, userID)
		}
		return repo.User{}, fmt.Errorf("get user: %w", err)
	}
	if user.Status == "banned" {
		return repo.User{}, fmt.Errorf("%w: %s", ErrUserBanned, userID)
	}
	if scope == "account" && user.UserType == "guest" {
		return repo.User{}, NewServiceError("guest users cannot receive account credentials")
	}
	return user, nil
}

// createScopedCredential generates a fresh raw token, persists only its
// hash (plus the scope/binding/validity-window metadata), and returns the
// raw token — the only place in the Service the raw token value exists
// outside of GenerateToken itself.
func (s *Service) createScopedCredential(
	ctx context.Context,
	userID, deviceLabel, scope string,
	sessionID, attachmentID *string,
	notBefore time.Time,
	expiresAt *time.Time,
) (string, error) {
	token, err := GenerateToken()
	if err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	params := repo.CreateCredentialParams{
		ID:           uuid.NewString(),
		UserID:       userID,
		TokenHash:    HashToken(token),
		DeviceLabel:  deviceLabel,
		Scope:        scope,
		SessionID:    toNullString(sessionID),
		AttachmentID: toNullString(attachmentID),
		NotBefore:    sql.NullString{String: formatTimestamp(notBefore), Valid: true},
	}
	if expiresAt != nil {
		params.ExpiresAt = sql.NullString{String: formatTimestamp(*expiresAt), Valid: true}
	}

	if _, err := s.q.CreateCredential(ctx, params); err != nil {
		return "", fmt.Errorf("create credential: %w", err)
	}
	return token, nil
}

// IssueAccountCredential issues a 30-day, unbound ("scope=account")
// credential for userID. Guest users are rejected.
func (s *Service) IssueAccountCredential(ctx context.Context, userID, deviceLabel string) (string, error) {
	if _, err := s.requireActiveUser(ctx, userID, "account"); err != nil {
		return "", err
	}

	issuedAt := s.now()
	expiresAt := issuedAt.Add(accountCredentialTTL)
	return s.createScopedCredential(ctx, userID, deviceLabel, "account", nil, nil, issuedAt, &expiresAt)
}

// IssueAttachmentCredential issues a credential bound to sessionID and
// attachmentID, scoped to expire at the caller-supplied expiresAt (which
// must be strictly in the future and at most 24h out). Guest users ARE
// allowed attachment credentials.
func (s *Service) IssueAttachmentCredential(ctx context.Context, userID, sessionID, attachmentID string, expiresAt time.Time) (string, error) {
	if _, err := s.requireActiveUser(ctx, userID, "attachment"); err != nil {
		return "", err
	}

	issuedAt := s.now()
	if !expiresAt.After(issuedAt) || expiresAt.After(issuedAt.Add(attachmentCredentialMaxTTL)) {
		return "", NewServiceError("attachment credential expiration must be within 24 hours")
	}

	status, err := s.q.GetSessionStatusForAuth(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", NewServiceError("session is not active: " + sessionID)
		}
		return "", fmt.Errorf("get session status: %w", err)
	}
	if status != "active" {
		return "", NewServiceError("session is not active: " + sessionID)
	}

	attachment, err := s.q.GetAttachmentForAuth(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", NewServiceError("attachment is not bound to the credential user and session")
		}
		return "", fmt.Errorf("get attachment: %w", err)
	}
	if attachment.SessionID != sessionID || !attachment.UserID.Valid || attachment.UserID.String != userID {
		return "", NewServiceError("attachment is not bound to the credential user and session")
	}

	return s.createScopedCredential(ctx, userID, "attachment", "attachment", &sessionID, &attachmentID, issuedAt, &expiresAt)
}

// RotateAttachmentCredential issues a fresh attachment credential with a
// full 24h TTL from now, and revokes the old one. It performs no
// transaction of its own: per the TS source's comment, the caller owns the
// surrounding transaction so attachment state and credential rotation
// commit together.
func (s *Service) RotateAttachmentCredential(ctx context.Context, credentialID, userID, sessionID, attachmentID string) (string, error) {
	cred, err := s.q.GetCredentialByID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: attachment credential is not valid for rotation", ErrCredentialNotFound)
		}
		return "", fmt.Errorf("get credential: %w", err)
	}
	if cred.RevokedAt.Valid ||
		cred.Scope != "attachment" ||
		cred.UserID != userID ||
		!cred.SessionID.Valid || cred.SessionID.String != sessionID ||
		!cred.AttachmentID.Valid || cred.AttachmentID.String != attachmentID {
		return "", fmt.Errorf("%w: attachment credential is not valid for rotation", ErrCredentialNotFound)
	}

	if !cred.ExpiresAt.Valid {
		return "", fmt.Errorf("%w: attachment credential is expired", ErrCredentialNotFound)
	}
	expiresAt, err := parseTimestamp(cred.ExpiresAt.String)
	if err != nil || !expiresAt.After(s.now()) {
		return "", fmt.Errorf("%w: attachment credential is expired", ErrCredentialNotFound)
	}

	newExpiresAt := s.now().Add(attachmentCredentialMaxTTL)
	newToken, err := s.IssueAttachmentCredential(ctx, userID, sessionID, attachmentID, newExpiresAt)
	if err != nil {
		return "", err
	}

	if err := s.q.RevokeCredential(ctx, credentialID); err != nil {
		return "", fmt.Errorf("revoke old credential: %w", err)
	}
	return newToken, nil
}

// Authenticate is the core bearer-token validator. It returns (nil, nil) —
// never an error — for every failure mode, so the middleware (Task 10) can
// produce one uniform 401 regardless of cause. A non-nil error return means
// something went wrong at the infrastructure level (e.g. a DB error other
// than "not found"), which the middleware should treat as a 500, not a 401.
func (s *Service) Authenticate(ctx context.Context, token string) (*AuthenticatedUser, error) {
	cred, err := s.q.GetCredentialByTokenHash(ctx, HashToken(token))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get credential by token hash: %w", err)
	}

	now := s.now()
	if cred.NotBefore.Valid {
		notBefore, err := parseTimestamp(cred.NotBefore.String)
		if err != nil || now.Before(notBefore) {
			return nil, nil
		}
	}
	if cred.ExpiresAt.Valid {
		expiresAt, err := parseTimestamp(cred.ExpiresAt.String)
		if err != nil || !now.Before(expiresAt) {
			return nil, nil
		}
	}

	user, err := s.q.GetUserByID(ctx, cred.UserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get user: %w", err)
	}
	if user.Status == "banned" {
		return nil, nil
	}

	switch cred.Scope {
	case "account":
		if user.UserType == "guest" || cred.SessionID.Valid || cred.AttachmentID.Valid {
			return nil, nil
		}
	case "attachment":
		if !cred.SessionID.Valid || !cred.AttachmentID.Valid {
			return nil, nil
		}
		status, err := s.q.GetSessionStatusForAuth(ctx, cred.SessionID.String)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, fmt.Errorf("get session status: %w", err)
		}
		if status != "active" {
			return nil, nil
		}
		attachment, err := s.q.GetAttachmentForAuth(ctx, cred.AttachmentID.String)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, nil
			}
			return nil, fmt.Errorf("get attachment: %w", err)
		}
		if attachment.SessionID != cred.SessionID.String || !attachment.UserID.Valid || attachment.UserID.String != user.ID {
			return nil, nil
		}
	default:
		return nil, nil
	}

	var sessionID, attachmentID *string
	if cred.SessionID.Valid {
		sid := cred.SessionID.String
		sessionID = &sid
	}
	if cred.AttachmentID.Valid {
		aid := cred.AttachmentID.String
		attachmentID = &aid
	}

	return &AuthenticatedUser{
		UserID:       user.ID,
		CredentialID: cred.ID,
		Scope:        cred.Scope,
		SessionID:    sessionID,
		AttachmentID: attachmentID,
	}, nil
}

// RevokeCurrentCredential unconditionally revokes credentialID with no
// ownership check — by design, this is only ever called with the caller's
// OWN resolved credential id from their authenticated context (logout),
// never a user-supplied id.
func (s *Service) RevokeCurrentCredential(ctx context.Context, credentialID string) error {
	if _, err := s.q.GetCredentialByID(ctx, credentialID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrCredentialNotFound, credentialID)
		}
		return fmt.Errorf("get credential: %w", err)
	}
	if err := s.q.RevokeCredential(ctx, credentialID); err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	return nil
}

// RevokeCredential is the self-service "revoke one of my other devices"
// path: it requires the requester to own the credential being revoked,
// distinct from RevokeCurrentCredential (logout) above.
func (s *Service) RevokeCredential(ctx context.Context, requesterUserID, credentialID string) error {
	cred, err := s.q.GetCredentialByID(ctx, credentialID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrCredentialNotFound, credentialID)
		}
		return fmt.Errorf("get credential: %w", err)
	}
	if cred.UserID != requesterUserID {
		return fmt.Errorf("%w: %s", ErrCredentialNotOwned, credentialID)
	}
	if err := s.q.RevokeCredential(ctx, credentialID); err != nil {
		return fmt.Errorf("revoke credential: %w", err)
	}
	return nil
}
