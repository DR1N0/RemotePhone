// Package auth ports the pre-Go TypeScript AuthService to Go. Service is the
// central credential/identity authority: it issues and
// validates bearer tokens, and is the only place password hashes, the
// bootstrap secret, and raw tokens are ever handled together.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strings"
	"time"

	"remotephone/internal/controlplane/persistence/repo"

	"github.com/google/uuid"
)

// accountCredentialTTL and attachmentCredentialMaxTTL mirror the TS
// constants ACCOUNT_CREDENTIAL_TTL_MS / ATTACHMENT_CREDENTIAL_MAX_TTL_MS.
const (
	accountCredentialTTL       = 30 * 24 * time.Hour
	attachmentCredentialMaxTTL = 24 * time.Hour

	// SystemUserID is the reserved principal used by the loopback owner
	// console for audit/actor FKs. It has no username or password and cannot
	// authenticate.
	SystemUserID       = "_SYSTEM"
	SystemCredentialID = "_SYSTEM"
)

// registrationInviteTTL is fixed, not caller-configurable — same reasoning
// as session.sessionInviteTTL: a 4-digit code has far less entropy (10,000
// possible values) than this package's other bearer tokens, so the validity
// window is part of what keeps redemption safe, not just a UX knob.
const registrationInviteTTL = 5 * time.Minute

// usernamePattern is the normalized-username validity pattern: lowercase
// letters or digits first, then letters, digits, and underscores, 3-32
// characters. Leading underscores are reserved for internal identities such
// as _SYSTEM.
var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{2,31}$`)

// isValidUsername normalizes username (trim whitespace, lowercase) and
// reports whether the normalized form satisfies usernamePattern. Used by
// both CreateRegistrationInvite's callers and Task 9's Register — do not
// duplicate this logic elsewhere.
func isValidUsername(username string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(username))
	return normalized, usernamePattern.MatchString(normalized)
}

// isValidPassword reports whether password's byte length falls within
// [8, 1024]. This matches the common case of the TS code's UTF-16
// code-unit length check; Unicode passwords with multi-byte characters are
// an accepted, minor fidelity gap versus the TS source.
func isValidPassword(password string) bool {
	return len(password) >= 8 && len(password) <= 1024
}

// Service is the Go port of the TS AuthService. NewService constructs it;
// Tasks 7-9 add more methods to this same type (SetupAdmin, Login,
// CreateRegistrationInvite, Register) — do not redefine it.
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
	// SQLite only supports one writer at a time regardless of how many
	// Go-level pooled connections exist; a larger pool only produces more
	// SQLITE_BUSY lock-contention errors under concurrent writers, never
	// more real write parallelism. Serializing all access through a single
	// connection lets concurrent transactions queue cleanly instead of
	// erroring out, so the loser of a race gets a clean constraint
	// violation (mapped to a sentinel error below) rather than a raw
	// "database is locked" error.
	db.SetMaxOpenConns(1)

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
	return s.GetUserTx(ctx, s.q, userID)
}

// GetUserByUsername resolves a username for the loopback owner console and
// admin code creation.
func (s *Service) GetUserByUsername(ctx context.Context, username string) (UserDTO, error) {
	user, err := s.q.GetUserByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserDTO{}, ErrUserNotFound
		}
		return UserDTO{}, fmt.Errorf("get user by username: %w", err)
	}
	return ToUserDTO(user), nil
}

// GetUserTx is GetUser against an explicit q — for callers (session.Service's
// invite redemption) that must read a user row created earlier in the SAME
// still-open transaction (e.g. a just-created guest user), for the same
// pool-size-1 reason as IssueAttachmentCredentialTx.
func (s *Service) GetUserTx(ctx context.Context, q *repo.Queries, userID string) (UserDTO, error) {
	row, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return UserDTO{}, fmt.Errorf("%w: %s", ErrUserNotFound, userID)
		}
		return UserDTO{}, fmt.Errorf("get user: %w", err)
	}
	return ToUserDTO(row), nil
}

// requireActiveUser loads userID and checks it is eligible to receive a
// credential of the given scope: not found -> ErrUserNotFound; banned ->
// ErrUserBanned; guest user requesting an "account" scoped credential ->
// ServiceError (guests ARE allowed "attachment" scoped credentials).
func (s *Service) requireActiveUser(ctx context.Context, userID, scope string) (repo.User, error) {
	return s.requireActiveUserTx(ctx, s.q, userID, scope)
}

// requireActiveUserTx is requireActiveUser against an explicit q, so callers
// holding their own transaction (e.g. session.Service.CreateSession, which
// must see its own as-yet-uncommitted session/attachment rows) can run this
// check without a second connection — see issueAttachmentCredentialTx.
func (s *Service) requireActiveUserTx(ctx context.Context, q *repo.Queries, userID, scope string) (repo.User, error) {
	user, err := q.GetUserByID(ctx, userID)
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

// RequireActiveAdmin loads actorUserID and checks it is an active admin:
// not found -> ErrUserNotFound; found but not an active admin (either
// userType != "admin" or status != "active") -> ErrNotAdmin. A banned admin
// gets the SAME ErrNotAdmin as a non-admin user — this intentionally does
// not distinguish "admin but banned" from "not admin at all", matching the
// TS source exactly. Exported (not just s.q-bound) so callers outside this
// package — namely the admin package — can run the check against their own
// transaction-scoped *repo.Queries, same as requireActiveUserTx.
func (s *Service) RequireActiveAdmin(ctx context.Context, q *repo.Queries, actorUserID string) (repo.User, error) {
	user, err := q.GetUserByID(ctx, actorUserID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.User{}, fmt.Errorf("%w: %s", ErrUserNotFound, actorUserID)
		}
		return repo.User{}, fmt.Errorf("get user: %w", err)
	}
	if (user.UserType != "admin" && user.UserType != "system") || user.Status != "active" {
		return repo.User{}, fmt.Errorf("%w: %s", ErrNotAdmin, actorUserID)
	}
	return user, nil
}

// createScopedCredential generates a fresh raw token, persists only its
// hash (plus the scope/binding/validity-window metadata), and returns the
// raw token — the only place in the Service the raw token value exists
// outside of GenerateToken itself. It takes an explicit q so callers running
// inside a transaction (e.g. SetupAdmin) can pass the transaction-scoped
// *repo.Queries instead of s.q, while non-transactional callers just pass
// s.q.
func (s *Service) createScopedCredential(
	ctx context.Context,
	q *repo.Queries,
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

	if _, err := q.CreateCredential(ctx, params); err != nil {
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
	return s.createScopedCredential(ctx, s.q, userID, deviceLabel, "account", nil, nil, issuedAt, &expiresAt)
}

// IssueAttachmentCredential issues a credential bound to sessionID and
// attachmentID, scoped to expire at the caller-supplied expiresAt (which
// must be strictly in the future and at most 24h out). Guest users ARE
// allowed attachment credentials.
func (s *Service) IssueAttachmentCredential(ctx context.Context, userID, sessionID, attachmentID string, expiresAt time.Time) (string, error) {
	return s.issueAttachmentCredentialTx(ctx, s.q, userID, sessionID, attachmentID, expiresAt)
}

// IssueAttachmentCredentialTx is IssueAttachmentCredential against an
// explicit q, for callers (session.Service.CreateSession) that must issue
// the credential inside their OWN open transaction so session+attachment+
// credential either all commit or none do — matching the TS source, where
// every DB operation during one synchronous better-sqlite3 transaction
// callback is atomic together regardless of which service object issued
// it. Go's explicit *sql.Tx threading has no equivalent for free, hence
// this method: s.q (used by the plain IssueAttachmentCredential) is bound
// to the top-level *sql.DB, a different connection-pool slot than a
// transaction's *sql.Tx — calling it while a caller's transaction holds the
// (pool-size-1, see NewService) only connection would deadlock, not just
// miss uncommitted rows.
func (s *Service) IssueAttachmentCredentialTx(ctx context.Context, q *repo.Queries, userID, sessionID, attachmentID string, expiresAt time.Time) (string, error) {
	return s.issueAttachmentCredentialTx(ctx, q, userID, sessionID, attachmentID, expiresAt)
}

func (s *Service) issueAttachmentCredentialTx(ctx context.Context, q *repo.Queries, userID, sessionID, attachmentID string, expiresAt time.Time) (string, error) {
	if _, err := s.requireActiveUserTx(ctx, q, userID, "attachment"); err != nil {
		return "", err
	}

	issuedAt := s.now()
	if !expiresAt.After(issuedAt) || expiresAt.After(issuedAt.Add(attachmentCredentialMaxTTL)) {
		return "", NewServiceError("attachment credential expiration must be within 24 hours")
	}

	status, err := q.GetSessionStatusForAuth(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", NewServiceError("session is not active: " + sessionID)
		}
		return "", fmt.Errorf("get session status: %w", err)
	}
	if status != "active" {
		return "", NewServiceError("session is not active: " + sessionID)
	}

	attachment, err := q.GetAttachmentForAuth(ctx, attachmentID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", NewServiceError("attachment is not bound to the credential user and session")
		}
		return "", fmt.Errorf("get attachment: %w", err)
	}
	if attachment.SessionID != sessionID || !attachment.UserID.Valid || attachment.UserID.String != userID {
		return "", NewServiceError("attachment is not bound to the credential user and session")
	}

	return s.createScopedCredential(ctx, q, userID, "attachment", "attachment", &sessionID, &attachmentID, issuedAt, &expiresAt)
}

// RotateAttachmentCredential issues a fresh attachment credential with a
// full 24h TTL from now, and revokes the old one.
func (s *Service) RotateAttachmentCredential(ctx context.Context, credentialID, userID, sessionID, attachmentID string) (string, error) {
	return s.rotateAttachmentCredentialTx(ctx, s.q, credentialID, userID, sessionID, attachmentID)
}

// RotateAttachmentCredentialTx is RotateAttachmentCredential against an
// explicit q — see IssueAttachmentCredentialTx's doc comment for why this
// exists. session.Service.ResumeAttachment calls this from within its own
// transaction so the credential rotation and the attachment's
// status/owner-absence-deadline updates commit together.
func (s *Service) RotateAttachmentCredentialTx(ctx context.Context, q *repo.Queries, credentialID, userID, sessionID, attachmentID string) (string, error) {
	return s.rotateAttachmentCredentialTx(ctx, q, credentialID, userID, sessionID, attachmentID)
}

func (s *Service) rotateAttachmentCredentialTx(ctx context.Context, q *repo.Queries, credentialID, userID, sessionID, attachmentID string) (string, error) {
	cred, err := q.GetCredentialByID(ctx, credentialID)
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
	newToken, err := s.issueAttachmentCredentialTx(ctx, q, userID, sessionID, attachmentID, newExpiresAt)
	if err != nil {
		return "", err
	}

	if err := q.RevokeCredential(ctx, credentialID); err != nil {
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

// withTx runs fn inside a database transaction scoped to transaction-local
// queries, committing on success and rolling back on any error (including
// re-panicking after rollback on a panic). Every read/write inside fn must
// go through the *repo.Queries it receives, not s.q, or it won't see a
// consistent, isolated view of in-flight changes.
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

// SetupAdminInput is the input to SetupAdmin. Neither Username nor Password
// shape is validated here — that is the HTTP route's job (Task 11); this
// method trusts its caller already validated them.
type SetupAdminInput struct {
	Username        string
	Password        string
	DeviceLabel     string
	BootstrapSecret string
}

// SetupAdmin performs the one-time first-admin bootstrap: it is only ever
// usable once, guarded by both the "no registered/admin user yet" check and
// the bootstrap secret. The registered/admin check is performed twice —
// once outside the transaction (cheap early rejection) and once again
// inside it (closes the TOCTOU race between the first check and the
// transaction actually starting) — so concurrent callers converge on
// exactly one winner.
func (s *Service) SetupAdmin(ctx context.Context, input SetupAdminInput) (UserDTO, string, error) {
	count, err := s.q.CountRegisteredOrAdminUsers(ctx)
	if err != nil {
		return UserDTO{}, "", fmt.Errorf("count registered or admin users: %w", err)
	}
	if count > 0 {
		return UserDTO{}, "", ErrServerAlreadyClaimed
	}

	if s.bootstrapSecret == nil {
		return UserDTO{}, "", ErrBootstrapSecretUnavailable
	}
	matches, err := s.bootstrapSecret.Matches(input.BootstrapSecret)
	if err != nil {
		return UserDTO{}, "", err
	}
	if !matches {
		return UserDTO{}, "", ErrBootstrapSecretInvalid
	}

	// HashPassword is slow by design (scrypt) — do it outside the
	// transaction so we're not holding a write lock while it runs.
	passwordHash, err := HashPassword(input.Password)
	if err != nil {
		return UserDTO{}, "", fmt.Errorf("hash password: %w", err)
	}

	var user repo.User
	var token string
	err = s.withTx(ctx, func(q *repo.Queries) error {
		count, err := q.CountRegisteredOrAdminUsers(ctx)
		if err != nil {
			return fmt.Errorf("count registered or admin users: %w", err)
		}
		if count > 0 {
			return ErrServerAlreadyClaimed
		}

		user, err = q.CreateUser(ctx, repo.CreateUserParams{
			ID:           uuid.NewString(),
			DisplayName:  input.Username,
			UserType:     "admin",
			Username:     sql.NullString{String: input.Username, Valid: true},
			PasswordHash: sql.NullString{String: passwordHash, Valid: true},
			Status:       "active",
		})
		if err != nil {
			return fmt.Errorf("create user: %w", err)
		}

		issuedAt := s.now()
		expiresAt := issuedAt.Add(accountCredentialTTL)
		credToken, err := s.createScopedCredential(ctx, q, user.ID, input.DeviceLabel, "account", nil, nil, issuedAt, &expiresAt)
		if err != nil {
			return err
		}
		token = credToken
		return nil
	})
	if err != nil {
		return UserDTO{}, "", err
	}

	// Only invalidate the bootstrap secret AFTER the transaction has
	// committed successfully: the user/credential are already durable at
	// this point, so a crash here is survivable — any later SetupAdmin call
	// hits the already-claimed check above before touching the secret
	// again.
	if err := s.bootstrapSecret.Invalidate(input.BootstrapSecret); err != nil {
		return UserDTO{}, "", err
	}

	return ToUserDTO(user), token, nil
}

// LoginInput is the input to Login. Neither Username nor Password shape is
// validated here — that is the HTTP route's job (Task 11).
type LoginInput struct {
	Username    string
	Password    string
	DeviceLabel string
}

// Login authenticates a username/password pair and, on success, issues a
// fresh account credential (never reuses or extends an existing one).
//
// The anti-enumeration check below is intentionally a single boolean
// expression rather than sequential early returns: an unknown username and
// a known username with a wrong password must take observably similar code
// paths and produce a byte-identical error, or the API becomes a user
// enumeration oracle. Go's || short-circuits left-to-right exactly like the
// TS source's, so the expensive VerifyPassword (scrypt) call only runs once
// every cheaper check has already passed.
func (s *Service) Login(ctx context.Context, input LoginInput) (UserDTO, string, error) {
	user, err := s.q.GetUserByUsername(ctx, input.Username)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UserDTO{}, "", fmt.Errorf("get user by username: %w", err)
	}

	if err != nil || user.Status != "active" || !user.PasswordHash.Valid || !VerifyPassword(input.Password, user.PasswordHash.String) {
		return UserDTO{}, "", ErrInvalidLogin
	}

	issuedAt := s.now()
	expiresAt := issuedAt.Add(accountCredentialTTL)
	token, err := s.createScopedCredential(ctx, s.q, user.ID, input.DeviceLabel, "account", nil, nil, issuedAt, &expiresAt)
	if err != nil {
		return UserDTO{}, "", err
	}

	return ToUserDTO(user), token, nil
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

// GenerateRegistrationInviteCode returns a 4-digit numeric code, zero-padded
// ("0042"), drawn from crypto/rand — short and easy to read aloud or type,
// matching session.generateSessionInvitePIN's convention. Exported so admin
// tooling/tests can predict the format without reaching into this package's
// internals.
func GenerateRegistrationInviteCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10_000))
	if err != nil {
		return "", fmt.Errorf("generate registration invite code: %w", err)
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

// CreateRegistrationInvite issues a new registration invite on behalf of
// actorUserID, who must be an active admin. The invite row and its audit
// event are written in the same transaction: both commit, or neither does.
// Returns the raw (unhashed) invite code and its expiry.
func (s *Service) CreateRegistrationInvite(ctx context.Context, actorUserID string, actorCredentialID *string, requestID string) (string, time.Time, error) {
	if _, err := s.RequireActiveAdmin(ctx, s.q, actorUserID); err != nil {
		return "", time.Time{}, err
	}

	issuedAt := s.now()
	expiresAt := issuedAt.Add(registrationInviteTTL)

	token, err := GenerateRegistrationInviteCode()
	if err != nil {
		return "", time.Time{}, err
	}

	err = s.withTx(ctx, func(q *repo.Queries) error {
		invite, err := q.CreateRegistrationInvite(ctx, repo.CreateRegistrationInviteParams{
			ID:        uuid.NewString(),
			TokenHash: HashToken(token),
			CreatedBy: actorUserID,
			ExpiresAt: formatTimestamp(expiresAt),
		})
		if err != nil {
			return fmt.Errorf("create registration invite: %w", err)
		}

		if err := q.CreateAuditEvent(ctx, repo.CreateAuditEventParams{
			ID:                uuid.NewString(),
			ActorUserID:       sql.NullString{String: actorUserID, Valid: true},
			ActorCredentialID: toNullString(actorCredentialID),
			Action:            "registration.invite.issue",
			TargetType:        "registration_invite",
			TargetID:          invite.ID,
			RequestID:         requestID,
			Outcome:           "success",
		}); err != nil {
			return fmt.Errorf("create audit event: %w", err)
		}
		return nil
	})
	if err != nil {
		return "", time.Time{}, err
	}

	return token, expiresAt, nil
}

// registrationInviteValid reports whether invite is still usable as of at:
// not consumed, and not yet expired (expiresAt strictly after at). Shared by
// Register's non-transactional pre-check and its transaction-scoped
// re-check so the two can never drift apart.
func registrationInviteValid(invite repo.RegistrationInvite, at time.Time) bool {
	if invite.ConsumedAt.Valid {
		return false
	}
	expiresAt, err := parseTimestamp(invite.ExpiresAt)
	if err != nil {
		return false
	}
	return expiresAt.After(at)
}

// mapCreateUserError maps a CreateUser failure caused by the username
// UNIQUE index (principals_username_ci, case-insensitive) to the same
// ErrUsernameConflict the step 4b pre-check above would have returned, so a
// caller who loses the race between 4b's read and this INSERT still gets a
// clean sentinel instead of a raw, driver-specific SQL error. Under this
// Service's actual connection topology (NewService caps the pool at a
// single connection, so writer transactions fully serialize) step 4b
// already catches essentially every real conflict before CreateUser ever
// runs, making this a backstop for conditions outside that normal
// single-process topology (e.g. a future change to the pool size, or a
// second process sharing the same database file) rather than the primary
// defense. String-matching the driver error here is a deliberate, narrow
// exception to this codebase's general anti-pattern of collapsing distinct
// application-level conditions via message matching: this is a single
// DB-level constraint violation with no cleaner cross-driver detection
// available via database/sql. err must be non-nil.
func mapCreateUserError(err error) error {
	if strings.Contains(err.Error(), "UNIQUE constraint failed") {
		return ErrUsernameConflict
	}
	return fmt.Errorf("create user: %w", err)
}

// Register redeems a registration invite into a new "registered" user and
// issues it an account credential. The invite is validated twice before any
// write — once outside the transaction (so an obviously dead invite never
// pays for the scrypt password hash below) and once again inside the
// transaction (closing the TOCTOU window between the pre-check and the
// transaction actually starting) — but neither check is the actual
// concurrency safety net: that is ConsumeRegistrationInvite's atomic
// conditional UPDATE and its affected-row count, checked in step 4 below.
// Only that check is race-safe; the two prior ones are pure fail-fast
// optimizations and MUST NOT be treated as sufficient on their own.
func (s *Service) Register(ctx context.Context, inviteToken, username, password, deviceLabel, requestID string) (UserDTO, string, error) {
	normalizedUsername, ok := isValidUsername(username)
	if !ok || !isValidPassword(password) {
		return UserDTO{}, "", NewServiceError("invalid registration credentials")
	}

	inviteTokenHash := HashToken(inviteToken)

	// Step 2: non-transactional pre-check — fail fast, before hashing.
	precheckedInvite, err := s.q.GetRegistrationInviteByTokenHash(ctx, inviteTokenHash)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return UserDTO{}, "", fmt.Errorf("get registration invite: %w", err)
	}
	if err != nil || !registrationInviteValid(precheckedInvite, s.now()) {
		return UserDTO{}, "", ErrRegistrationInviteInvalid
	}

	// HashPassword is slow by design (scrypt) — do it outside the
	// transaction, same reasoning as SetupAdmin.
	passwordHash, err := HashPassword(password)
	if err != nil {
		return UserDTO{}, "", fmt.Errorf("hash password: %w", err)
	}

	var user repo.User
	var token string
	err = s.withTx(ctx, func(q *repo.Queries) error {
		registeredAt := s.now()

		// Step 4a: re-check the same conditions, transaction-scoped.
		invite, err := q.GetRegistrationInviteByTokenHash(ctx, inviteTokenHash)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrRegistrationInviteInvalid
			}
			return fmt.Errorf("get registration invite: %w", err)
		}
		if !registrationInviteValid(invite, registeredAt) {
			return ErrRegistrationInviteInvalid
		}

		// Step 4b: username availability.
		if _, err := q.GetUserByUsername(ctx, normalizedUsername); err == nil {
			return ErrUsernameConflict
		} else if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("get user by username: %w", err)
		}

		// Step 4c: create the user. The username-availability check above
		// (step 4b) is a fail-fast optimization, not the real concurrency
		// safety net — two concurrent Register calls can both pass it for
		// the same desired username before either commits. The backstop is
		// this UNIQUE constraint violation: if the database rejects the
		// insert because another transaction won the race, map it to the
		// same ErrUsernameConflict step 4b would have returned, rather than
		// propagating the raw SQL error (which, depending on the Go SQLite
		// driver's connection-pool contention behavior, can otherwise
		// surface as an unrelated "database is locked" error instead of a
		// clean conflict). String-matching the driver error here is a
		// legitimate, deliberate exception to this codebase's general
		// anti-pattern of collapsing distinct application-level conditions
		// via message matching: this is a single DB-level constraint
		// violation with no cleaner cross-driver detection available via
		// database/sql.
		user, err = q.CreateUser(ctx, repo.CreateUserParams{
			ID:           uuid.NewString(),
			DisplayName:  normalizedUsername,
			UserType:     "registered",
			Username:     sql.NullString{String: normalizedUsername, Valid: true},
			PasswordHash: sql.NullString{String: passwordHash, Valid: true},
			Status:       "active",
		})
		if err != nil {
			return mapCreateUserError(err)
		}

		// Step 4d: atomically consume the invite. This is the ACTUAL
		// concurrency safety net — the WHERE clause's own
		// "consumed_at IS NULL AND expires_at > ?" re-validates freshness
		// as part of the same atomic statement, and a 0 affected-row count
		// here means someone else's concurrent Register call won the race
		// between step 4a's read and this UPDATE, even though both step 2
		// and step 4a already passed.
		affected, err := q.ConsumeRegistrationInvite(ctx, repo.ConsumeRegistrationInviteParams{
			ConsumedAt:       sql.NullString{String: formatTimestamp(registeredAt), Valid: true},
			ConsumedByUserID: sql.NullString{String: user.ID, Valid: true},
			ID:               invite.ID,
			ExpiresAt:        formatTimestamp(registeredAt),
		})
		if err != nil {
			return fmt.Errorf("consume registration invite: %w", err)
		}
		if affected == 0 {
			return ErrRegistrationInviteInvalid
		}

		// Step 4e: audit event.
		if err := q.CreateAuditEvent(ctx, repo.CreateAuditEventParams{
			ID:          uuid.NewString(),
			ActorUserID: sql.NullString{String: user.ID, Valid: true},
			Action:      "registration.invite.redeem",
			TargetType:  "registration_invite",
			TargetID:    invite.ID,
			RequestID:   requestID,
			Outcome:     "success",
		}); err != nil {
			return fmt.Errorf("create audit event: %w", err)
		}

		// Step 4f: issue an account credential, same shape as
		// IssueAccountCredential (inlined because that method's
		// requireActiveUser check reads via s.q, not the tx-scoped q).
		expiresAt := registeredAt.Add(accountCredentialTTL)
		credToken, err := s.createScopedCredential(ctx, q, user.ID, deviceLabel, "account", nil, nil, registeredAt, &expiresAt)
		if err != nil {
			return err
		}
		token = credToken
		return nil
	})
	if err != nil {
		return UserDTO{}, "", err
	}

	return ToUserDTO(user), token, nil
}
