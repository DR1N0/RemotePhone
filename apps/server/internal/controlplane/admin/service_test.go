package admin

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/workerproc"

	_ "modernc.org/sqlite"
)

func newTestServices(t *testing.T) (*Service, *auth.Service, *session.Service, *sql.DB) {
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
	authSvc, err := auth.NewService(db, "")
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	sessionSvc := session.NewService(db, authSvc, &session.KeyedMutex{}, time.Minute)
	// ListAllInstances (the only instance.Service method these tests
	// exercise) is a pure DB read — the placement selector is never
	// actually called, a fake Execer is enough.
	placementSelector := placement.NewSelector(&workerproc.FakeExecer{}, "fake-mako-worker")
	instanceSvc := instance.NewService(db, placementSelector)
	adminSvc := NewService(db, authSvc, sessionSvc, instanceSvc)
	return adminSvc, authSvc, sessionSvc, db
}

func testAuditContext() audit.Context {
	return audit.NewContext(nil, nil, "test-request")
}

func createTestUser(t *testing.T, db *sql.DB, id, userType, status string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, display_name, user_type, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, id, userType, status,
	); err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
}

func createTestInstance(t *testing.T, db *sql.DB, id, ownerID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state, created_at, updated_at)
		 VALUES (?, ?, 'test-instance', 'android-studio-emulator', 't1', '{"kind":"local-only"}', '{}', '{}', 'stopped', 'stopped', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, ownerID,
	); err != nil {
		t.Fatalf("createTestInstance: %v", err)
	}
}

// bootstrapOwnedSession creates ownerID (an active registered user), an
// instance it owns, and a session on that instance with ownerID's sole
// attachment in role "owner". Returns the session and attachment ids.
func bootstrapOwnedSession(t *testing.T, authSvc *auth.Service, sessionSvc *session.Service, db *sql.DB, ownerID string) (sessionID, attachmentID string) {
	t.Helper()
	createTestUser(t, db, ownerID, "registered", "active")
	instanceID := ownerID + "-instance"
	createTestInstance(t, db, instanceID, ownerID)
	token, err := authSvc.IssueAccountCredential(context.Background(), ownerID, "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	ownerCaller, err := authSvc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	record, _, _, err := sessionSvc.CreateSession(context.Background(), instanceID, ownerID+"-client", nil, *ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return record.ID, record.Attachments[0].ID
}

func TestRequireAdminRejectsNonAdmin(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "u-1", "registered", "active")

	if err := adminSvc.RequireAdmin(context.Background(), "u-1"); !errors.Is(err, auth.ErrNotAdmin) {
		t.Errorf("RequireAdmin() error = %v, want ErrNotAdmin", err)
	}
}

func TestListUsersExcludesGuestsAndPaginates(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestUser(t, db, "reg-1", "registered", "active")
	createTestUser(t, db, "guest-1", "guest", "active")

	page1, cursor1, err := adminSvc.ListUsers(context.Background(), "admin-1", 1, nil)
	if err != nil {
		t.Fatalf("ListUsers page 1: %v", err)
	}
	if len(page1) != 1 || cursor1 == nil {
		t.Fatalf("ListUsers page 1 = %+v, cursor=%v, want 1 item and a cursor", page1, cursor1)
	}

	page2, cursor2, err := adminSvc.ListUsers(context.Background(), "admin-1", 10, cursor1)
	if err != nil {
		t.Fatalf("ListUsers page 2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("ListUsers page 2 = %+v, want 1 item (guest excluded)", page2)
	}
	if cursor2 != nil {
		t.Errorf("cursor2 = %v, want nil", *cursor2)
	}
	for _, u := range append(page1, page2...) {
		if u.Type == "guest" {
			t.Errorf("ListUsers included a guest: %+v", u)
		}
	}
}

func TestSetUserTypePromoteAndDemote(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestUser(t, db, "reg-1", "registered", "active")

	if err := adminSvc.SetUserType(context.Background(), "reg-1", "admin", "admin-1", testAuditContext()); err != nil {
		t.Fatalf("SetUserType(promote): %v", err)
	}
	users, _, err := adminSvc.ListUsers(context.Background(), "admin-1", 10, nil)
	if err != nil {
		t.Fatalf("ListUsers: %v", err)
	}
	var gotType string
	for _, u := range users {
		if u.ID == "reg-1" {
			gotType = u.Type
		}
	}
	if gotType != "admin" {
		t.Errorf("reg-1 type after promote = %q, want admin", gotType)
	}
}

func TestSetUserTypeRefusesToDemoteFinalActiveAdmin(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")

	err := adminSvc.SetUserType(context.Background(), "admin-1", "registered", "admin-1", testAuditContext())
	if !errors.Is(err, ErrLastActiveAdmin) {
		t.Errorf("SetUserType(self-demote, sole admin) error = %v, want ErrLastActiveAdmin", err)
	}
}

func TestListCredentialsAndRevokeAccountCredential(t *testing.T) {
	adminSvc, authSvc, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestUser(t, db, "reg-1", "registered", "active")
	token, err := authSvc.IssueAccountCredential(context.Background(), "reg-1", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}

	creds, err := adminSvc.ListCredentials(context.Background(), "admin-1", "reg-1")
	if err != nil {
		t.Fatalf("ListCredentials: %v", err)
	}
	if len(creds) != 1 {
		t.Fatalf("ListCredentials() = %+v, want 1 credential", creds)
	}

	if err := adminSvc.RevokeAccountCredential(context.Background(), "reg-1", creds[0].ID, "admin-1", testAuditContext()); err != nil {
		t.Fatalf("RevokeAccountCredential: %v", err)
	}
	if authed, err := authSvc.Authenticate(context.Background(), token); err != nil || authed != nil {
		t.Errorf("Authenticate(revoked credential) = (%+v, %v), want (nil, nil)", authed, err)
	}
}

func TestListInstancesSpansOwners(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestUser(t, db, "u-1", "registered", "active")
	createTestInstance(t, db, "i-1", "u-1")

	items, err := adminSvc.ListInstances(context.Background(), "admin-1")
	if err != nil {
		t.Fatalf("ListInstances: %v", err)
	}
	if len(items) != 1 || items[0].ID != "i-1" {
		t.Fatalf("ListInstances() = %+v, want [i-1]", items)
	}
}

func TestListAuditReturnsAdminMutationEvents(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	createTestUser(t, db, "reg-1", "registered", "active")
	if err := adminSvc.SetUserType(context.Background(), "reg-1", "admin", "admin-1", testAuditContext()); err != nil {
		t.Fatalf("SetUserType: %v", err)
	}

	items, _, err := adminSvc.ListAudit(context.Background(), "admin-1", 10, nil)
	if err != nil {
		t.Fatalf("ListAudit: %v", err)
	}
	if len(items) != 1 || items[0].Action != actionUserPromote || items[0].TargetID != "reg-1" {
		t.Fatalf("ListAudit() = %+v, want one user.promote/reg-1 event", items)
	}
}

func TestSetBanStatusClosesOwnedSessionsAndRevokesOtherAttachments(t *testing.T) {
	adminSvc, authSvc, sessionSvc, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	ownedSessionID, ownedAttachmentID := bootstrapOwnedSession(t, authSvc, sessionSvc, db, "target")

	targets, err := adminSvc.SetBanStatus(context.Background(), "target", "banned", "admin-1", testAuditContext())
	if err != nil {
		t.Fatalf("SetBanStatus: %v", err)
	}
	if len(targets) != 1 || targets[0].SessionID != ownedSessionID || targets[0].AttachmentID != ownedAttachmentID {
		t.Fatalf("SetBanStatus() targets = %+v, want [{%s %s}]", targets, ownedSessionID, ownedAttachmentID)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), ownedSessionID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "closed" {
		t.Errorf("session status after ban = %q, want closed", after.Status)
	}
}

func TestSetBanStatusRefusesToBanFinalActiveAdmin(t *testing.T) {
	adminSvc, _, _, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")

	_, err := adminSvc.SetBanStatus(context.Background(), "admin-1", "banned", "admin-1", testAuditContext())
	if !errors.Is(err, ErrLastActiveAdmin) {
		t.Errorf("SetBanStatus(self-ban, sole admin) error = %v, want ErrLastActiveAdmin", err)
	}
}

func TestRevokeGuestAttachment(t *testing.T) {
	adminSvc, authSvc, sessionSvc, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	sessionID, ownerAttachmentID := bootstrapOwnedSession(t, authSvc, sessionSvc, db, "owner")
	createTestUser(t, db, "guest-1", "guest", "active")
	if _, err := db.Exec(
		`INSERT INTO attachments (id, session_id, client_id, user_id, role, status, created_at, updated_at)
		 VALUES ('guest-attachment', ?, 'guest-client', 'guest-1', 'participant', 'connected', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		sessionID,
	); err != nil {
		t.Fatalf("insert guest attachment: %v", err)
	}

	target, err := adminSvc.RevokeGuestAttachment(context.Background(), sessionID, "guest-attachment", "admin-1", testAuditContext())
	if err != nil {
		t.Fatalf("RevokeGuestAttachment: %v", err)
	}
	if target.SessionID != sessionID || target.AttachmentID != "guest-attachment" {
		t.Errorf("RevokeGuestAttachment() = %+v, want {%s guest-attachment}", target, sessionID)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), sessionID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "active" {
		t.Errorf("session status after guest revoke = %q, want still active", after.Status)
	}
	var ownerStillConnected bool
	for _, a := range after.Attachments {
		if a.ID == ownerAttachmentID {
			ownerStillConnected = a.Status == "connected"
		}
	}
	if !ownerStillConnected {
		t.Errorf("owner attachment affected by guest revoke, want untouched")
	}
}

func TestRevokeGuestAttachmentRejectsNonGuest(t *testing.T) {
	adminSvc, authSvc, sessionSvc, db := newTestServices(t)
	createTestUser(t, db, "admin-1", "admin", "active")
	sessionID, ownerAttachmentID := bootstrapOwnedSession(t, authSvc, sessionSvc, db, "owner")

	_, err := adminSvc.RevokeGuestAttachment(context.Background(), sessionID, ownerAttachmentID, "admin-1", testAuditContext())
	if !errors.Is(err, ErrGuestAttachmentNotFound) {
		t.Errorf("RevokeGuestAttachment(owner attachment) error = %v, want ErrGuestAttachmentNotFound", err)
	}
}
