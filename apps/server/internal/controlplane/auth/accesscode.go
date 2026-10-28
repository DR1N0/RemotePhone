package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"remotephone/internal/controlplane/persistence/repo"
)

var (
	ErrAccessCodeInvalid = errors.New("access code is invalid or expired")
	ErrAccessCodeExpired = errors.New("access code has expired")
	ErrAccessCodeUsed    = errors.New("access code has already been used")
)

const (
	accessCodeMaxTTL      = 24 * time.Hour
	accessCodeMaxFailures = 5
	accessCodeAlphabet    = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"
)

type accessCodeRow struct {
	ID             string
	TargetUserID   sql.NullString
	ExpiresAt      string
	UsedAt         sql.NullString
	FailedAttempts int
}

func normalizeAccessCode(raw string) (string, bool) {
	normalized := strings.ToUpper(strings.TrimSpace(raw))
	normalized = strings.ReplaceAll(normalized, "-", "")
	normalized = strings.ReplaceAll(normalized, " ", "")
	// Crockford-style human transcription: O -> 0, I/L -> 1.
	normalized = strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(normalized)
	if len(normalized) < 6 || len(normalized) > 32 {
		return "", false
	}
	for _, r := range normalized {
		if !strings.ContainsRune(accessCodeAlphabet, r) {
			return "", false
		}
	}
	return normalized, true
}

func generateAccessCode() (string, error) {
	var b strings.Builder
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(accessCodeAlphabet))))
		if err != nil {
			return "", err
		}
		b.WriteByte(accessCodeAlphabet[n.Int64()])
	}
	raw := b.String()
	return raw[:4] + "-" + raw[4:], nil
}

func (s *Service) lookupAccessCode(ctx context.Context, q repo.DBTX, normalized string) (accessCodeRow, error) {
	row := accessCodeRow{}
	err := q.QueryRowContext(ctx, `
		SELECT id, target_user_id, expires_at, used_at, failed_attempts
		FROM access_codes WHERE code_hash = ?`, HashToken(normalized)).Scan(
		&row.ID, &row.TargetUserID, &row.ExpiresAt, &row.UsedAt, &row.FailedAttempts,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return accessCodeRow{}, ErrAccessCodeInvalid
	}
	if err != nil {
		return accessCodeRow{}, fmt.Errorf("lookup access code: %w", err)
	}
	return row, nil
}

func accessCodeState(row accessCodeRow, now time.Time) error {
	if row.UsedAt.Valid {
		return ErrAccessCodeUsed
	}
	expiresAt, err := parseTimestamp(row.ExpiresAt)
	if err != nil {
		return fmt.Errorf("parse access code expiry: %w", err)
	}
	if !now.Before(expiresAt) {
		return ErrAccessCodeExpired
	}
	if row.FailedAttempts >= accessCodeMaxFailures {
		return ErrAccessCodeInvalid
	}
	return nil
}

// IssueAccessCode creates a single-use code. targetUserID nil means the code
// can create a new ordinary user or pair an existing user; a set target binds
// the code to that account. The caller (HTTP layer) is responsible for
// deciding whether the caller is allowed to choose this target.
func (s *Service) IssueAccessCode(ctx context.Context, targetUserID *string, createdByUserID *string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = 10 * time.Minute
	}
	if ttl > accessCodeMaxTTL {
		ttl = accessCodeMaxTTL
	}
	var target sql.NullString
	if targetUserID != nil {
		if _, err := s.q.GetUserByID(ctx, *targetUserID); err != nil {
			return "", time.Time{}, fmt.Errorf("access code target: %w", err)
		}
		target = sql.NullString{String: *targetUserID, Valid: true}
	}
	var createdBy sql.NullString
	if createdByUserID != nil {
		createdBy = sql.NullString{String: *createdByUserID, Valid: true}
	}
	expiresAt := s.now().Add(ttl)

	for attempt := 0; attempt < 5; attempt++ {
		code, err := generateAccessCode()
		if err != nil {
			return "", time.Time{}, err
		}
		normalized, _ := normalizeAccessCode(code)
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO access_codes
				(id, code_hash, target_user_id, created_by_user_id, expires_at)
			VALUES (?, ?, ?, ?, ?)`,
			uuid.NewString(), HashToken(normalized), target, createdBy, formatTimestamp(expiresAt),
		)
		if err == nil {
			return code, expiresAt, nil
		}
		if !strings.Contains(strings.ToLower(err.Error()), "unique") {
			return "", time.Time{}, fmt.Errorf("insert access code: %w", err)
		}
	}
	return "", time.Time{}, errors.New("could not allocate a unique access code")
}

// SeedBootstrapAccessCode replaces the local bootstrap code so the value
// printed at startup can always pair one device. It remains single-use.
func (s *Service) SeedBootstrapAccessCode(ctx context.Context, code string, ttl time.Duration) error {
	normalized, ok := normalizeAccessCode(code)
	if !ok {
		return fmt.Errorf("invalid bootstrap access code")
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM access_codes WHERE code_hash = ?`, HashToken(normalized)); err != nil {
		return fmt.Errorf("reset bootstrap access code: %w", err)
	}
	_, _, err := s.issueAccessCodeWithValue(ctx, code, nil, nil, ttl)
	return err
}

func (s *Service) issueAccessCodeWithValue(ctx context.Context, code string, target *string, createdBy *string, ttl time.Duration) (string, time.Time, error) {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	if ttl > accessCodeMaxTTL {
		ttl = accessCodeMaxTTL
	}
	normalized, ok := normalizeAccessCode(code)
	if !ok {
		return "", time.Time{}, fmt.Errorf("invalid access code")
	}
	var targetNull sql.NullString
	if target != nil {
		targetNull = sql.NullString{String: *target, Valid: true}
	}
	var createdByNull sql.NullString
	if createdBy != nil {
		createdByNull = sql.NullString{String: *createdBy, Valid: true}
	}
	expiresAt := s.now().Add(ttl)
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO access_codes
			(id, code_hash, target_user_id, created_by_user_id, expires_at)
		VALUES (?, ?, ?, ?, ?)`,
		uuid.NewString(), HashToken(normalized), targetNull, createdByNull, formatTimestamp(expiresAt),
	)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("insert access code: %w", err)
	}
	return code, expiresAt, nil
}

// RedeemAccessCode consumes a code and either pairs an existing account or
// (for an unbound code) creates a new ordinary account. The result indicates
// whether an account was created.
func (s *Service) RedeemAccessCode(ctx context.Context, code, username, password, deviceLabel string) (UserDTO, string, bool, error) {
	normalized, ok := normalizeAccessCode(code)
	if !ok {
		return UserDTO{}, "", false, ErrAccessCodeInvalid
	}
	normalizedUsername, ok := isValidUsername(username)
	if !ok || !isValidPassword(password) || strings.TrimSpace(deviceLabel) == "" {
		return UserDTO{}, "", false, NewServiceError("invalid access code redemption request")
	}
	now := s.now()
	row, err := s.lookupAccessCode(ctx, s.db, normalized)
	if err != nil {
		return UserDTO{}, "", false, err
	}
	if err := accessCodeState(row, now); err != nil {
		return UserDTO{}, "", false, err
	}

	var targetUser repo.User
	if row.TargetUserID.Valid {
		targetUser, err = s.q.GetUserByID(ctx, row.TargetUserID.String)
		if err != nil {
			return UserDTO{}, "", false, ErrAccessCodeInvalid
		}
		if targetUser.Status != "active" ||
			!targetUser.Username.Valid ||
			!strings.EqualFold(strings.TrimSpace(targetUser.Username.String), normalizedUsername) {
			return UserDTO{}, "", false, ErrAccessCodeInvalid
		}
	} else {
		targetUser, err = s.q.GetUserByUsername(ctx, normalizedUsername)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return UserDTO{}, "", false, fmt.Errorf("get access code user: %w", err)
		}
	}

	// Unbound codes may create a user, so hash the password before the
	// transaction (scrypt is intentionally slow). If the username turns out to
	// already exist, the hash is simply unused. Bound codes only pair an
	// existing account and verify against its stored hash.
	var passwordHash string
	if !row.TargetUserID.Valid {
		passwordHash, err = HashPassword(password)
		if err != nil {
			return UserDTO{}, "", false, fmt.Errorf("hash password: %w", err)
		}
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserDTO{}, "", false, fmt.Errorf("begin access code transaction: %w", err)
	}
	defer tx.Rollback()
	q := repo.New(tx)

	locked, err := s.lookupAccessCode(ctx, tx, normalized)
	if err != nil {
		return UserDTO{}, "", false, err
	}
	if err := accessCodeState(locked, s.now()); err != nil {
		return UserDTO{}, "", false, err
	}

	var user repo.User
	created := false
	if locked.TargetUserID.Valid {
		user, err = q.GetUserByID(ctx, locked.TargetUserID.String)
		if err != nil || user.Status != "active" || !user.Username.Valid ||
			!strings.EqualFold(strings.TrimSpace(user.Username.String), normalizedUsername) {
			return UserDTO{}, "", false, ErrAccessCodeInvalid
		}
	} else {
		user, err = q.GetUserByUsername(ctx, normalizedUsername)
		switch {
		case err == nil:
			// Existing account: pair a new device.
		case errors.Is(err, sql.ErrNoRows):
			user, err = q.CreateUser(ctx, repo.CreateUserParams{
				ID:           uuid.NewString(),
				DisplayName:  normalizedUsername,
				UserType:     "registered",
				Username:     sql.NullString{String: normalizedUsername, Valid: true},
				PasswordHash: sql.NullString{String: passwordHash, Valid: true},
				Status:       "active",
			})
			if err != nil {
				return UserDTO{}, "", false, fmt.Errorf("create access code user: %w", err)
			}
			created = true
		default:
			return UserDTO{}, "", false, fmt.Errorf("get access code user: %w", err)
		}
	}

	if !created {
		if user.Status != "active" || !user.PasswordHash.Valid || !VerifyPassword(password, user.PasswordHash.String) {
			if err := s.recordAccessCodeFailure(ctx, tx, locked); err != nil {
				return UserDTO{}, "", false, err
			}
			if err := tx.Commit(); err != nil {
				return UserDTO{}, "", false, fmt.Errorf("commit failed access attempt: %w", err)
			}
			return UserDTO{}, "", false, ErrInvalidLogin
		}
	}

	issuedAt := s.now()
	expiresAt := issuedAt.Add(accountCredentialTTL)
	token, err := s.createScopedCredential(ctx, q, user.ID, deviceLabel, "account", nil, nil, issuedAt, &expiresAt)
	if err != nil {
		return UserDTO{}, "", false, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE access_codes SET used_at = ? WHERE id = ? AND used_at IS NULL`,
		formatTimestamp(issuedAt), locked.ID); err != nil {
		return UserDTO{}, "", false, fmt.Errorf("consume access code: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return UserDTO{}, "", false, fmt.Errorf("commit access code redemption: %w", err)
	}
	return ToUserDTO(user), token, created, nil
}

func (s *Service) recordAccessCodeFailure(ctx context.Context, tx *sql.Tx, row accessCodeRow) error {
	attempts := row.FailedAttempts + 1
	var usedAt any
	if attempts >= accessCodeMaxFailures {
		usedAt = formatTimestamp(s.now())
	}
	_, err := tx.ExecContext(ctx, `
		UPDATE access_codes SET failed_attempts = ?, used_at = COALESCE(?, used_at)
		WHERE id = ?`, attempts, usedAt, row.ID)
	return err
}
