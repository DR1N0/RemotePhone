package admin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/instance"
	"remotephone/internal/controlplane/persistence/repo"
	"remotephone/internal/controlplane/session"
)

const (
	actionUserPromote          = "user.promote"
	actionUserDemote           = "user.demote"
	actionUserBan              = "user.ban"
	actionUserUnban            = "user.unban"
	actionUserCredentialRevoke = "user.credential.revoke"
	actionAdminGuestRevoke     = "admin.guest.revoke"
)

// Service implements the admin package's cross-owner operations. It holds
// its own *repo.Queries (like session.Service and instance.Service) plus
// references to authSvc (for RequireActiveAdmin), sessionSvc (for
// CloseSessionTx, the ban flow's owner-session closure), and instanceSvc
// (for ListAllInstances) — deliberately not transport.Service; see doc.go.
type Service struct {
	db          *sql.DB
	q           *repo.Queries
	authSvc     *auth.Service
	sessionSvc  *session.Service
	instanceSvc *instance.Service
}

// NewService constructs a Service backed by db, which MUST be the same
// *sql.DB authSvc/sessionSvc/instanceSvc were constructed with — the ban
// flow calls sessionSvc.CloseSessionTx from inside its own transaction,
// which only works against the same connection pool (see
// session.Service.NewService's doc comment for why).
func NewService(db *sql.DB, authSvc *auth.Service, sessionSvc *session.Service, instanceSvc *instance.Service) *Service {
	return &Service{
		db:          db,
		q:           repo.New(db),
		authSvc:     authSvc,
		sessionSvc:  sessionSvc,
		instanceSvc: instanceSvc,
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

// RequireAdmin reports whether callerUserID is an active admin — the
// adminapi requireAdmin route wrapper's single check.
func (s *Service) RequireAdmin(ctx context.Context, callerUserID string) error {
	_, err := s.authSvc.RequireActiveAdmin(ctx, s.q, callerUserID)
	return err
}

func requireTarget(ctx context.Context, q *repo.Queries, userID string) (repo.User, error) {
	user, err := q.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return repo.User{}, fmt.Errorf("%w: %s", auth.ErrUserNotFound, userID)
		}
		return repo.User{}, fmt.Errorf("get user: %w", err)
	}
	return user, nil
}

// requireNotFinalActiveAdmin blocks a mutation that would take the last
// active admin out of that status (demoted, or banned) — mirrors TS's
// requireNotFinalActiveAdmin exactly, including applying regardless of
// which half (type or status) of "active admin" the mutation changes.
func requireNotFinalActiveAdmin(ctx context.Context, q *repo.Queries, target repo.User, nextType, nextStatus string) error {
	if target.UserType != "admin" || target.Status != "active" {
		return nil
	}
	if nextType == "admin" && nextStatus == "active" {
		return nil
	}
	count, err := q.CountActiveAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count active admins: %w", err)
	}
	if count <= 1 {
		return ErrLastActiveAdmin
	}
	return nil
}

// ListUsers returns a cursor-paginated page of non-guest users, oldest id
// first — matches ListAccountUsers' ORDER BY id ASC.
func (s *Service) ListUsers(ctx context.Context, callerUserID string, limit int, cursor *string) ([]auth.UserDTO, *string, error) {
	if _, err := s.authSvc.RequireActiveAdmin(ctx, s.q, callerUserID); err != nil {
		return nil, nil, err
	}
	var cursorID interface{}
	if cursor != nil {
		cursorID = *cursor
	}
	rows, err := s.q.ListAccountUsers(ctx, repo.ListAccountUsersParams{CursorID: cursorID, Limit: int64(limit) + 1})
	if err != nil {
		return nil, nil, fmt.Errorf("list account users: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	items := make([]auth.UserDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, auth.ToUserDTO(row))
	}
	var nextCursor *string
	if hasMore {
		nc := rows[len(rows)-1].ID
		nextCursor = &nc
	}
	return items, nextCursor, nil
}

// ListCredentials returns targetUserID's account-scoped credentials.
func (s *Service) ListCredentials(ctx context.Context, callerUserID, targetUserID string) ([]CredentialDto, error) {
	if _, err := s.authSvc.RequireActiveAdmin(ctx, s.q, callerUserID); err != nil {
		return nil, err
	}
	if _, err := requireTarget(ctx, s.q, targetUserID); err != nil {
		return nil, err
	}
	rows, err := s.q.ListAccountCredentialsForUser(ctx, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("list account credentials for user: %w", err)
	}
	items := make([]CredentialDto, 0, len(rows))
	for _, row := range rows {
		items = append(items, toCredentialDto(row))
	}
	return items, nil
}

// ListInstances returns every instance across all owners.
func (s *Service) ListInstances(ctx context.Context, callerUserID string) ([]instance.ManagedInstance, error) {
	if _, err := s.authSvc.RequireActiveAdmin(ctx, s.q, callerUserID); err != nil {
		return nil, err
	}
	return s.instanceSvc.ListAllInstances(ctx)
}

// ListAudit returns a cursor-paginated page of audit events, newest first.
func (s *Service) ListAudit(ctx context.Context, callerUserID string, limit int, cursor *string) ([]audit.Event, *string, error) {
	if _, err := s.authSvc.RequireActiveAdmin(ctx, s.q, callerUserID); err != nil {
		return nil, nil, err
	}
	return audit.List(ctx, s.q, limit, cursor)
}

// RevokeAccountCredential revokes one of targetUserID's account-scoped
// credentials.
func (s *Service) RevokeAccountCredential(ctx context.Context, targetUserID, credentialID, callerUserID string, actor audit.Context) error {
	return s.withTx(ctx, func(q *repo.Queries) error {
		if _, err := s.authSvc.RequireActiveAdmin(ctx, q, callerUserID); err != nil {
			return err
		}
		if _, err := requireTarget(ctx, q, targetUserID); err != nil {
			return err
		}
		cred, err := q.GetAccountCredentialForUser(ctx, repo.GetAccountCredentialForUserParams{ID: credentialID, UserID: targetUserID})
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrAccountCredentialNotFound, credentialID)
			}
			return fmt.Errorf("get account credential for user: %w", err)
		}
		if err := q.RevokeCredential(ctx, cred.ID); err != nil {
			return fmt.Errorf("revoke credential: %w", err)
		}
		return audit.Record(ctx, q, actor, actionUserCredentialRevoke, "credential", cred.ID, audit.OutcomeSuccess)
	})
}

// SetUserType promotes/demotes targetUserID, refusing to demote the final
// active admin (requireNotFinalActiveAdmin).
func (s *Service) SetUserType(ctx context.Context, targetUserID, userType, callerUserID string, actor audit.Context) error {
	return s.withTx(ctx, func(q *repo.Queries) error {
		if _, err := s.authSvc.RequireActiveAdmin(ctx, q, callerUserID); err != nil {
			return err
		}
		target, err := requireTarget(ctx, q, targetUserID)
		if err != nil {
			return err
		}
		if err := requireNotFinalActiveAdmin(ctx, q, target, userType, target.Status); err != nil {
			return err
		}
		if err := q.UpdateUserType(ctx, repo.UpdateUserTypeParams{UserType: userType, ID: target.ID}); err != nil {
			return fmt.Errorf("update user type: %w", err)
		}
		action := actionUserDemote
		if userType == "admin" {
			action = actionUserPromote
		}
		return audit.Record(ctx, q, actor, action, "user", target.ID, audit.OutcomeSuccess)
	})
}
