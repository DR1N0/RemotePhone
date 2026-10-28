package auth

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

func newTestService(t *testing.T) *Service {
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
	svc, err := NewService(db, "") // no bootstrap secret needed for these tests
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc
}

// createTestUser inserts a user row via the service's own repo.Queries
// (svc.q is reachable because this test file is in package auth).
func createTestUser(t *testing.T, svc *Service, id, userType, status string) {
	t.Helper()
	_, err := svc.q.CreateUser(context.Background(), repo.CreateUserParams{
		ID: id, DisplayName: id, UserType: userType, Status: status,
	})
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
}

// banTestUser flips a user's status directly via raw SQL — there is no
// production UpdateUserStatus query in this phase (that belongs to a later
// admin-service port), so the test reaches into the DB directly rather than
// inventing a query this phase's AuthService never actually calls.
func banTestUser(t *testing.T, svc *Service, id string) {
	t.Helper()
	if _, err := svc.db.Exec(`UPDATE users SET status = 'banned' WHERE id = ?`, id); err != nil {
		t.Fatalf("banTestUser: %v", err)
	}
}

// createTestSessionAndAttachment inserts a minimal valid instance, session,
// and attachment row via raw SQL, for tests that only need
// IssueAttachmentCredential's session/attachment EXISTENCE and OWNERSHIP
// checks to pass — full session/instance CRUD is a later phase, so this
// helper exists only to support this phase's own tests, not as production
// code.
func createTestSessionAndAttachment(t *testing.T, svc *Service, ownerUserID, sessionID, attachmentID, attachmentUserID string) {
	t.Helper()
	db := svc.db
	if _, err := db.Exec(
		`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state, created_at, updated_at)
		 VALUES (?, ?, 'test-instance', 'android-studio-emulator', 't1', '{"kind":"local-only"}', '{}', '{}', 'stopped', 'stopped', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"i1", ownerUserID,
	); err != nil {
		t.Fatalf("insert test instance: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO sessions (id, instance_id, status, created_at, updated_at)
		 VALUES (?, 'i1', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		sessionID,
	); err != nil {
		t.Fatalf("insert test session: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO attachments (id, session_id, user_id, client_id, role, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'client1', 'owner', 'connected', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		attachmentID, sessionID, attachmentUserID,
	); err != nil {
		t.Fatalf("insert test attachment: %v", err)
	}
}

func TestIssueAndAuthenticateAccountCredential(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")

	token, err := svc.IssueAccountCredential(context.Background(), "u1", "my-device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}

	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed == nil {
		t.Fatal("Authenticate returned nil for a freshly issued, valid token")
	}
	if authed.UserID != "u1" || authed.Scope != "account" {
		t.Errorf("Authenticate() = %+v, want UserID=u1 Scope=account", authed)
	}
}

func TestAuthenticateRejectsUnknownToken(t *testing.T) {
	svc := newTestService(t)
	authed, err := svc.Authenticate(context.Background(), "this-token-was-never-issued")
	if err != nil {
		t.Fatalf("Authenticate returned an error (want nil, nil) for an unknown token: %v", err)
	}
	if authed != nil {
		t.Error("Authenticate() returned non-nil for an unknown token")
	}
}

func TestAuthenticateRejectsBannedUser(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	token, err := svc.IssueAccountCredential(context.Background(), "u1", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	// Ban the user AFTER issuing the credential — Authenticate must re-read
	// live status on every call, not anything cached at issuance time.
	banTestUser(t, svc, "u1")

	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed != nil {
		t.Error("Authenticate() succeeded for a banned user's still-otherwise-valid credential")
	}
}

func TestIssueAccountCredentialRejectsGuestUser(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "g1", "guest", "active")
	_, err := svc.IssueAccountCredential(context.Background(), "g1", "device")
	if err == nil {
		t.Fatal("IssueAccountCredential succeeded for a guest user, want an error")
	}
}

func TestIssueAttachmentCredentialBoundary(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestSessionAndAttachment(t, svc, "u1", "s1", "a1", "u1")
	// Pin the clock: the boundary being tested is only 1ms wide, and
	// IssueAttachmentCredential re-reads s.now() internally after several DB
	// round-trips per subtest below. Relying on real wall-clock time to stay
	// within 1ms of a snapshot taken before any of those round-trips is
	// flaky under -race (and generally); pinning now removes that source of
	// nondeterminism while still exercising the exact same boundary math.
	now := time.Now()
	svc.now = func() time.Time { return now }

	t.Run("exactly now+24h is accepted", func(t *testing.T) {
		_, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", now.Add(24*time.Hour))
		if err != nil {
			t.Errorf("IssueAttachmentCredential(now+24h) failed, want success: %v", err)
		}
	})

	t.Run("now+24h+1ms is rejected", func(t *testing.T) {
		_, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", now.Add(24*time.Hour+time.Millisecond))
		if err == nil {
			t.Error("IssueAttachmentCredential(now+24h+1ms) succeeded, want rejection")
		}
	})

	t.Run("exactly now is rejected (must be strictly after issuance)", func(t *testing.T) {
		_, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", now)
		if err == nil {
			t.Error("IssueAttachmentCredential(now) succeeded, want rejection")
		}
	})
}

func TestIssueAttachmentCredentialRejectsMismatchedOwnership(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestUser(t, svc, "u2", "registered", "active")
	createTestSessionAndAttachment(t, svc, "u1", "s1", "a1", "u1")
	now := time.Now()
	svc.now = func() time.Time { return now }

	// u2 did not create this attachment — must be rejected even though u2
	// is a perfectly valid, active, non-guest user.
	_, err := svc.IssueAttachmentCredential(context.Background(), "u2", "s1", "a1", now.Add(time.Hour))
	if err == nil {
		t.Error("IssueAttachmentCredential succeeded for a user who doesn't own the attachment")
	}
}

// --- Tests below this point are new, beyond the brief's literal listing,
// added to cover decision-tree order / boundary cases the brief calls out
// explicitly but does not give full source for. ---

func TestAuthenticateNotBeforeBoundaryInclusive(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	token, err := svc.IssueAccountCredential(context.Background(), "u1", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	// notBefore == now() at issuance time, so authenticating "now" (using the
	// service's own clock) must be >= notBefore and therefore pass.
	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed == nil {
		t.Error("Authenticate() failed at exactly notBefore, want success (inclusive boundary)")
	}
}

func TestAuthenticateExpiresAtBoundaryExclusive(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestSessionAndAttachment(t, svc, "u1", "s1", "a1", "u1")
	fixedNow := svc.now()
	svc.now = func() time.Time { return fixedNow }

	// Issue an attachment credential that expires in exactly 1 second.
	token, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", fixedNow.Add(time.Second))
	if err != nil {
		t.Fatalf("IssueAttachmentCredential: %v", err)
	}

	// Move the clock to exactly expiresAt: must FAIL (exclusive boundary).
	svc.now = func() time.Time { return fixedNow.Add(time.Second) }
	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed != nil {
		t.Error("Authenticate() succeeded at exactly expiresAt, want rejection (exclusive boundary)")
	}

	// One millisecond before expiresAt: must PASS.
	svc.now = func() time.Time { return fixedNow.Add(time.Second - time.Millisecond) }
	authed, err = svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed == nil {
		t.Error("Authenticate() failed 1ms before expiresAt, want success")
	}
}

func TestIssueAttachmentCredentialAllowsGuestUser(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "g1", "guest", "active")
	createTestSessionAndAttachment(t, svc, "g1", "s1", "a1", "g1")
	now := svc.now()

	token, err := svc.IssueAttachmentCredential(context.Background(), "g1", "s1", "a1", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueAttachmentCredential should allow guest users: %v", err)
	}
	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed == nil || authed.Scope != "attachment" {
		t.Errorf("Authenticate() = %+v, want attachment-scoped auth for guest", authed)
	}
	if authed.SessionID == nil || *authed.SessionID != "s1" {
		t.Errorf("Authenticate() SessionID = %v, want s1", authed.SessionID)
	}
	if authed.AttachmentID == nil || *authed.AttachmentID != "a1" {
		t.Errorf("Authenticate() AttachmentID = %v, want a1", authed.AttachmentID)
	}
}

func TestIssueAttachmentCredentialRejectsInactiveSession(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestSessionAndAttachment(t, svc, "u1", "s1", "a1", "u1")
	if _, err := svc.db.Exec(`UPDATE sessions SET status = 'ended' WHERE id = ?`, "s1"); err != nil {
		t.Fatalf("mark session ended: %v", err)
	}
	now := svc.now()
	_, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", now.Add(time.Hour))
	if err == nil {
		t.Error("IssueAttachmentCredential succeeded for an inactive session, want rejection")
	}
}

func TestAuthenticateRejectsAccountCredentialForGuest(t *testing.T) {
	// An account-scoped credential is only issuable for non-guest users, but
	// Authenticate must defend against it independently (e.g. a user that
	// was a registered user at issuance and is a guest now is impossible in
	// practice, but the decision tree must still check user type at auth
	// time per the brief's step 4 — this test exercises that check directly
	// by manufacturing the otherwise-impossible row state).
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	token, err := svc.IssueAccountCredential(context.Background(), "u1", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	if _, err := svc.db.Exec(`UPDATE users SET user_type = 'guest' WHERE id = ?`, "u1"); err != nil {
		t.Fatalf("flip user to guest: %v", err)
	}
	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed != nil {
		t.Error("Authenticate() succeeded for an account credential whose user is now a guest, want rejection")
	}
}

func TestRevokeCurrentCredential(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	token, err := svc.IssueAccountCredential(context.Background(), "u1", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil || authed == nil {
		t.Fatalf("Authenticate: %v, %v", authed, err)
	}

	if err := svc.RevokeCurrentCredential(context.Background(), authed.CredentialID); err != nil {
		t.Fatalf("RevokeCurrentCredential: %v", err)
	}

	authed2, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate after revoke: %v", err)
	}
	if authed2 != nil {
		t.Error("Authenticate() succeeded for a revoked credential")
	}

	if err := svc.RevokeCurrentCredential(context.Background(), "does-not-exist"); err == nil {
		t.Error("RevokeCurrentCredential succeeded for an unknown id, want ErrCredentialNotFound")
	}
}

func TestRevokeCredentialOwnershipCheck(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestUser(t, svc, "u2", "registered", "active")
	token, err := svc.IssueAccountCredential(context.Background(), "u1", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	authed, err := svc.Authenticate(context.Background(), token)
	if err != nil || authed == nil {
		t.Fatalf("Authenticate: %v, %v", authed, err)
	}

	if err := svc.RevokeCredential(context.Background(), "u2", authed.CredentialID); err == nil {
		t.Error("RevokeCredential succeeded for a non-owning requester, want ErrCredentialNotOwned")
	}

	if err := svc.RevokeCredential(context.Background(), "u1", authed.CredentialID); err != nil {
		t.Fatalf("RevokeCredential: %v", err)
	}

	authed2, err := svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate after revoke: %v", err)
	}
	if authed2 != nil {
		t.Error("Authenticate() succeeded for a credential revoked by its owner")
	}

	if err := svc.RevokeCredential(context.Background(), "u1", "does-not-exist"); err == nil {
		t.Error("RevokeCredential succeeded for an unknown id, want ErrCredentialNotFound")
	}
}

func TestRotateAttachmentCredential(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestSessionAndAttachment(t, svc, "u1", "s1", "a1", "u1")
	now := svc.now()

	oldToken, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueAttachmentCredential: %v", err)
	}
	oldAuthed, err := svc.Authenticate(context.Background(), oldToken)
	if err != nil || oldAuthed == nil {
		t.Fatalf("Authenticate: %v, %v", oldAuthed, err)
	}

	newToken, err := svc.RotateAttachmentCredential(context.Background(), oldAuthed.CredentialID, "u1", "s1", "a1")
	if err != nil {
		t.Fatalf("RotateAttachmentCredential: %v", err)
	}
	if newToken == oldToken {
		t.Error("RotateAttachmentCredential returned the same token as the old credential")
	}

	// Old credential must now be revoked.
	oldAgain, err := svc.Authenticate(context.Background(), oldToken)
	if err != nil {
		t.Fatalf("Authenticate(old): %v", err)
	}
	if oldAgain != nil {
		t.Error("old attachment credential still authenticates after rotation")
	}

	// New credential must be valid, fresh 24h TTL, same scope/binding.
	newAuthed, err := svc.Authenticate(context.Background(), newToken)
	if err != nil {
		t.Fatalf("Authenticate(new): %v", err)
	}
	if newAuthed == nil {
		t.Fatal("new attachment credential does not authenticate")
	}
	if newAuthed.Scope != "attachment" || newAuthed.SessionID == nil || *newAuthed.SessionID != "s1" {
		t.Errorf("Authenticate(new) = %+v, want attachment scope bound to s1/a1", newAuthed)
	}

	// Rotating again with a stale/mismatched credential id must fail.
	if _, err := svc.RotateAttachmentCredential(context.Background(), oldAuthed.CredentialID, "u1", "s1", "a1"); err == nil {
		t.Error("RotateAttachmentCredential succeeded for an already-revoked credential id, want ErrCredentialNotFound")
	}
}

func TestGetUserAndGetSetupStatus(t *testing.T) {
	svc := newTestService(t)
	claimed, err := svc.GetSetupStatus(context.Background())
	if err != nil {
		t.Fatalf("GetSetupStatus: %v", err)
	}
	if claimed {
		t.Error("GetSetupStatus() = true before any registered/admin user exists")
	}

	createTestUser(t, svc, "u1", "registered", "active")
	claimed, err = svc.GetSetupStatus(context.Background())
	if err != nil {
		t.Fatalf("GetSetupStatus: %v", err)
	}
	if !claimed {
		t.Error("GetSetupStatus() = false after a registered user exists")
	}

	dto, err := svc.GetUser(context.Background(), "u1")
	if err != nil {
		t.Fatalf("GetUser: %v", err)
	}
	if dto.ID != "u1" || dto.Type != "registered" || dto.Status != "active" {
		t.Errorf("GetUser() = %+v, want id=u1 type=registered status=active", dto)
	}

	if _, err := svc.GetUser(context.Background(), "does-not-exist"); err == nil {
		t.Error("GetUser succeeded for an unknown id, want ErrUserNotFound")
	}
}
