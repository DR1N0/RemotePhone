package persistence_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"remotephone/internal/controlplane/persistence"
	"remotephone/internal/controlplane/persistence/repo"

	_ "modernc.org/sqlite"
)

func setupTestDB(t *testing.T) *repo.Queries {
	t.Helper()
	_, q := setupTestDBWithConn(t)
	return q
}

func setupTestDBWithConn(t *testing.T) (*sql.DB, *repo.Queries) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := persistence.ApplyMigrations(db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	return db, repo.New(db)
}

func TestCreateAndGetUserByUsername(t *testing.T) {
	q := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, repo.CreateUserParams{
		ID: "u1", DisplayName: "Admin", UserType: "admin",
		Username:     sql.NullString{String: "Admin_One", Valid: true},
		PasswordHash: sql.NullString{String: "hash", Valid: true},
		Status:       "active",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if user.ID != "u1" {
		t.Fatalf("CreateUser returned id %q, want u1", user.ID)
	}

	got, err := q.GetUserByUsername(ctx, "Admin_One")
	if err != nil {
		t.Fatalf("GetUserByUsername (case-insensitive lookup): %v", err)
	}
	if got.ID != "u1" {
		t.Errorf("GetUserByUsername found id %q, want u1 (case-insensitive match failed)", got.ID)
	}

	// A differently-cased, differently-whitespaced lookup should still match,
	// since the query normalizes both sides with lower(trim(...)).
	got2, err := q.GetUserByUsername(ctx, "  admin_ONE  ")
	if err != nil {
		t.Fatalf("GetUserByUsername (case/whitespace-insensitive lookup): %v", err)
	}
	if got2.ID != "u1" {
		t.Errorf("GetUserByUsername found id %q, want u1 (case/whitespace-insensitive match failed)", got2.ID)
	}

	byID, err := q.GetUserByID(ctx, "u1")
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if byID.DisplayName != "Admin" || byID.UserType != "admin" {
		t.Errorf("GetUserByID() = %+v, want display_name=Admin user_type=admin", byID)
	}

	count, err := q.CountRegisteredOrAdminUsers(ctx)
	if err != nil {
		t.Fatalf("CountRegisteredOrAdminUsers: %v", err)
	}
	if count != 1 {
		t.Errorf("CountRegisteredOrAdminUsers() = %d, want 1", count)
	}
}

func TestCredentialLifecycle(t *testing.T) {
	q := setupTestDB(t)
	ctx := context.Background()

	user, err := q.CreateUser(ctx, repo.CreateUserParams{
		ID: "u1", DisplayName: "Admin", UserType: "admin", Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	cred, err := q.CreateCredential(ctx, repo.CreateCredentialParams{
		ID: "c1", UserID: user.ID, TokenHash: "tokhash1", DeviceLabel: "device-1", Scope: "account",
	})
	if err != nil {
		t.Fatalf("CreateCredential: %v", err)
	}
	if cred.ID != "c1" || cred.UserID != "u1" {
		t.Fatalf("CreateCredential() = %+v, want id=c1 user_id=u1", cred)
	}

	byHash, err := q.GetCredentialByTokenHash(ctx, "tokhash1")
	if err != nil {
		t.Fatalf("GetCredentialByTokenHash: %v", err)
	}
	if byHash.ID != "c1" {
		t.Errorf("GetCredentialByTokenHash() id = %q, want c1", byHash.ID)
	}

	byID, err := q.GetCredentialByID(ctx, "c1")
	if err != nil {
		t.Fatalf("GetCredentialByID: %v", err)
	}
	if byID.ID != "c1" {
		t.Errorf("GetCredentialByID() id = %q, want c1", byID.ID)
	}

	if err := q.RevokeCredential(ctx, "c1"); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}

	// A revoked credential must behave identically to "not found" for
	// GetCredentialByTokenHash, matching the TS code's authenticate() semantics.
	if _, err := q.GetCredentialByTokenHash(ctx, "tokhash1"); err != sql.ErrNoRows {
		t.Errorf("GetCredentialByTokenHash after revoke: got err %v, want sql.ErrNoRows", err)
	}

	// GetCredentialByID (not filtered by revoked_at) must still find it.
	afterRevoke, err := q.GetCredentialByID(ctx, "c1")
	if err != nil {
		t.Fatalf("GetCredentialByID after revoke: %v", err)
	}
	if !afterRevoke.RevokedAt.Valid {
		t.Errorf("GetCredentialByID after revoke: RevokedAt.Valid = false, want true")
	}
}

func TestConsumeRegistrationInviteConcurrencyPrimitive(t *testing.T) {
	q := setupTestDB(t)
	ctx := context.Background()

	admin, err := q.CreateUser(ctx, repo.CreateUserParams{
		ID: "admin1", DisplayName: "Admin", UserType: "admin", Status: "active",
	})
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	invite, err := q.CreateRegistrationInvite(ctx, repo.CreateRegistrationInviteParams{
		ID: "inv1", TokenHash: "hash1", CreatedBy: admin.ID, ExpiresAt: expiresAt,
	})
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}

	fromHash, err := q.GetRegistrationInviteByTokenHash(ctx, "hash1")
	if err != nil {
		t.Fatalf("GetRegistrationInviteByTokenHash: %v", err)
	}
	if fromHash.ID != invite.ID {
		t.Fatalf("GetRegistrationInviteByTokenHash() id = %q, want %q", fromHash.ID, invite.ID)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	affected, err := q.ConsumeRegistrationInvite(ctx, repo.ConsumeRegistrationInviteParams{
		ConsumedAt:       sql.NullString{String: now, Valid: true},
		ConsumedByUserID: sql.NullString{String: "newuser1", Valid: true},
		ID:               invite.ID,
		ExpiresAt:        now,
	})
	if err != nil {
		t.Fatalf("ConsumeRegistrationInvite (1st): %v", err)
	}
	if affected != 1 {
		t.Fatalf("first consumption affected %d rows, want 1", affected)
	}

	// A second consumption attempt of the SAME invite must affect 0 rows —
	// this is the actual concurrency-safety mechanism Task 9 relies on.
	affected, err = q.ConsumeRegistrationInvite(ctx, repo.ConsumeRegistrationInviteParams{
		ConsumedAt:       sql.NullString{String: now, Valid: true},
		ConsumedByUserID: sql.NullString{String: "someoneelse", Valid: true},
		ID:               invite.ID,
		ExpiresAt:        now,
	})
	if err != nil {
		t.Fatalf("ConsumeRegistrationInvite (2nd): %v", err)
	}
	if affected != 0 {
		t.Fatalf("second consumption of an already-consumed invite affected %d rows, want 0", affected)
	}
}

func TestAuthSessionChecks(t *testing.T) {
	rawDB, q := setupTestDBWithConn(t)
	ctx := context.Background()

	if _, err := q.CreateUser(ctx, repo.CreateUserParams{
		ID: "admin1", DisplayName: "Admin", UserType: "admin", Status: "active",
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// Seed an instance, session, and attachment directly via raw SQL since
	// full session/attachment CRUD is a later phase's concern — these two
	// queries only need rows to read back.
	if _, err := rawDB.ExecContext(ctx, `INSERT INTO instances (id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state, owner_id) VALUES ('inst1','n','rc','tpl','{}','{}','{}','running','running','admin1')`); err != nil {
		t.Fatalf("seed instance: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `INSERT INTO sessions (id, instance_id, status) VALUES ('sess1','inst1','active')`); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err := rawDB.ExecContext(ctx, `INSERT INTO attachments (id, session_id, client_id, role, status, user_id) VALUES ('att1','sess1','client1','owner','connected','admin1')`); err != nil {
		t.Fatalf("seed attachment: %v", err)
	}

	status, err := q.GetSessionStatusForAuth(ctx, "sess1")
	if err != nil {
		t.Fatalf("GetSessionStatusForAuth: %v", err)
	}
	if status != "active" {
		t.Errorf("GetSessionStatusForAuth() = %q, want active", status)
	}

	att, err := q.GetAttachmentForAuth(ctx, "att1")
	if err != nil {
		t.Fatalf("GetAttachmentForAuth: %v", err)
	}
	if att.SessionID != "sess1" || !att.UserID.Valid || att.UserID.String != "admin1" {
		t.Errorf("GetAttachmentForAuth() = %+v, want session_id=sess1 user_id=admin1", att)
	}
}
