package session

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/persistence/repo"

	_ "modernc.org/sqlite"
)

func newTestServices(t *testing.T) (*Service, *auth.Service, *sql.DB) {
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
	sessionSvc := NewService(db, authSvc, &KeyedMutex{}, time.Minute)
	return sessionSvc, authSvc, db
}

func testAuditContext() audit.Context {
	return audit.NewContext(nil, nil, "test-request")
}

func createTestUser(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, display_name, user_type, status, created_at, updated_at)
		 VALUES (?, ?, 'registered', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, id,
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

func accountCaller(userID, credentialID string) auth.AuthenticatedUser {
	return auth.AuthenticatedUser{UserID: userID, CredentialID: credentialID, Scope: "account"}
}

func attachmentCaller(userID, credentialID, sessionID, attachmentID string) auth.AuthenticatedUser {
	return auth.AuthenticatedUser{
		UserID: userID, CredentialID: credentialID, Scope: "attachment",
		SessionID: &sessionID, AttachmentID: &attachmentID,
	}
}

func mustAuthenticate(t *testing.T, authSvc *auth.Service, token string) auth.AuthenticatedUser {
	t.Helper()
	authed, err := authSvc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed == nil {
		t.Fatalf("Authenticate(%q) = nil, want a valid credential", token)
	}
	return *authed
}

func TestCreateSessionIssuesOwnerAttachmentAndCredential(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	createTestUser(t, db, "owner")
	createTestInstance(t, db, "i1", "owner")
	ownerAccountToken, err := authSvc.IssueAccountCredential(context.Background(), "owner", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	caller := mustAuthenticate(t, authSvc, ownerAccountToken)

	record, attachmentToken, err := sessionSvc.CreateSession(context.Background(), "i1", "owner-client", nil, caller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if record.InstanceID != "i1" || record.Status != "active" {
		t.Errorf("CreateSession record = %+v, want active session for i1", record)
	}
	if len(record.Attachments) != 1 || record.Attachments[0].Role != "owner" {
		t.Fatalf("CreateSession attachments = %+v, want one owner attachment", record.Attachments)
	}

	authed := mustAuthenticate(t, authSvc, attachmentToken)
	if authed.Scope != "attachment" || authed.SessionID == nil || *authed.SessionID != record.ID {
		t.Errorf("Authenticate(attachmentToken) = %+v, want attachment scope bound to session %s", authed, record.ID)
	}
}

func TestCreateSessionRejectsNonAccountCaller(t *testing.T) {
	sessionSvc, _, db := newTestServices(t)
	createTestUser(t, db, "owner")
	createTestInstance(t, db, "i1", "owner")

	_, _, err := sessionSvc.CreateSession(context.Background(), "i1", "client", nil, attachmentCaller("owner", "c1", "s0", "a0"))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateSession with attachment-scoped caller = %v, want ErrForbidden", err)
	}
}

func TestCreateSessionRejectsNonOwner(t *testing.T) {
	sessionSvc, _, db := newTestServices(t)
	createTestUser(t, db, "owner")
	createTestUser(t, db, "intruder")
	createTestInstance(t, db, "i1", "owner")

	_, _, err := sessionSvc.CreateSession(context.Background(), "i1", "client", nil, accountCaller("intruder", "c1"))
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateSession by non-owner = %v, want ErrForbidden", err)
	}
}

func TestCreateSessionInstanceNotFound(t *testing.T) {
	sessionSvc, _, db := newTestServices(t)
	createTestUser(t, db, "owner")

	_, _, err := sessionSvc.CreateSession(context.Background(), "missing", "client", nil, accountCaller("owner", "c1"))
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("CreateSession with missing instance = %v, want ErrSessionNotFound", err)
	}
}

func TestCreateSessionConflictsWithActiveSession(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	createTestUser(t, db, "owner")
	createTestInstance(t, db, "i1", "owner")
	token, _ := authSvc.IssueAccountCredential(context.Background(), "owner", "device")
	caller := mustAuthenticate(t, authSvc, token)

	if _, _, err := sessionSvc.CreateSession(context.Background(), "i1", "client", nil, caller); err != nil {
		t.Fatalf("first CreateSession: %v", err)
	}
	_, _, err := sessionSvc.CreateSession(context.Background(), "i1", "client", nil, caller)
	if !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("second CreateSession = %v, want ErrSessionConflict", err)
	}
}

// bootstrapSession creates owner user, instance, and session+owner
// attachment in one call, returning enough to drive further operations.
func bootstrapSession(t *testing.T, sessionSvc *Service, authSvc *auth.Service, db *sql.DB) (SessionRecord, string, auth.AuthenticatedUser) {
	t.Helper()
	createTestUser(t, db, "owner")
	createTestInstance(t, db, "i1", "owner")
	ownerToken, err := authSvc.IssueAccountCredential(context.Background(), "owner", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	ownerCaller := mustAuthenticate(t, authSvc, ownerToken)
	record, attachmentToken, err := sessionSvc.CreateSession(context.Background(), "i1", "owner-client", nil, ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return record, attachmentToken, ownerCaller
}

func TestConnectAttachmentMarksConnected(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]

	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, ownerCaller, testAuditContext()); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	got, err := sessionSvc.ConnectAttachment(context.Background(), record.ID, attachment.ID)
	if err != nil {
		t.Fatalf("ConnectAttachment: %v", err)
	}
	if got.Status != "connected" {
		t.Errorf("ConnectAttachment status = %q, want connected", got.Status)
	}
}

func TestDisconnectAttachmentByOwnerSetsAbsenceDeadlineAndRevokesCredential(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, attachmentToken, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]

	updated, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}
	if updated.OwnerAbsenceDeadline == nil {
		t.Errorf("DisconnectAttachment(owner) OwnerAbsenceDeadline = nil, want set")
	}
	if updated.Attachments[0].Status != "disconnected" {
		t.Errorf("attachment status = %q, want disconnected", updated.Attachments[0].Status)
	}

	authed, err := authSvc.Authenticate(context.Background(), attachmentToken)
	if err != nil || authed != nil {
		t.Errorf("Authenticate(revoked attachment token) = (%+v, %v), want (nil, nil)", authed, err)
	}
}

func TestDisconnectAttachmentBySelfDoesNotRevokeCredential(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, attachmentToken, _ := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]
	selfCaller := mustAuthenticate(t, authSvc, attachmentToken)

	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, selfCaller, testAuditContext()); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	authed, err := authSvc.Authenticate(context.Background(), attachmentToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed == nil {
		t.Errorf("Authenticate(self-disconnect token) = nil, want still valid (kickedByAccount=false)")
	}
}

func TestResumeAttachmentRotatesCredentialAndClearsOwnerAbsenceDeadline(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, attachmentToken, _ := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]
	selfCaller := mustAuthenticate(t, authSvc, attachmentToken)

	// A self-disconnect (not account-initiated) leaves the credential valid
	// but still sets the owner-absence deadline, since the owner attachment
	// just went offline either way.
	disconnected, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, selfCaller, testAuditContext())
	if err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}
	if disconnected.OwnerAbsenceDeadline == nil {
		t.Fatalf("precondition failed: expected owner absence deadline to be set")
	}

	resumed, newToken, err := sessionSvc.ResumeAttachment(context.Background(), record.ID, attachment.ID, selfCaller)
	if err != nil {
		t.Fatalf("ResumeAttachment: %v", err)
	}
	if resumed.Status != "connected" {
		t.Errorf("ResumeAttachment status = %q, want connected", resumed.Status)
	}

	if authed, err := authSvc.Authenticate(context.Background(), attachmentToken); err != nil || authed != nil {
		t.Errorf("Authenticate(old token) after ResumeAttachment = (%+v, %v), want (nil, nil) — revoked by rotation", authed, err)
	}
	if authed := mustAuthenticate(t, authSvc, newToken); authed.Scope != "attachment" {
		t.Errorf("Authenticate(newToken).Scope = %q, want attachment", authed.Scope)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.OwnerAbsenceDeadline != nil {
		t.Errorf("OwnerAbsenceDeadline after ResumeAttachment(owner) = %v, want cleared", *after.OwnerAbsenceDeadline)
	}
}

func TestRefreshAttachmentCredentialRotatesWithoutChangingStatus(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, attachmentToken, _ := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]
	caller := mustAuthenticate(t, authSvc, attachmentToken)

	newToken, err := sessionSvc.RefreshAttachmentCredential(context.Background(), record.ID, attachment.ID, caller)
	if err != nil {
		t.Fatalf("RefreshAttachmentCredential: %v", err)
	}
	if authed, err := authSvc.Authenticate(context.Background(), attachmentToken); err != nil || authed != nil {
		t.Errorf("Authenticate(old token) after refresh = (%+v, %v), want (nil, nil)", authed, err)
	}
	if authed := mustAuthenticate(t, authSvc, newToken); authed.Scope != "attachment" {
		t.Errorf("Authenticate(newToken).Scope = %q, want attachment", authed.Scope)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Attachments[0].Status != "connected" {
		t.Errorf("attachment status after refresh = %q, want unchanged (connected)", after.Attachments[0].Status)
	}
}

func TestGetSessionAuthorization(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, attachmentToken, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	createTestUser(t, db, "intruder")
	intruderToken, err := authSvc.IssueAccountCredential(context.Background(), "intruder", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	intruderCaller := mustAuthenticate(t, authSvc, intruderToken)
	attachCaller := mustAuthenticate(t, authSvc, attachmentToken)

	if _, err := sessionSvc.GetSession(context.Background(), record.ID, ownerCaller); err != nil {
		t.Errorf("GetSession(owner) = %v, want success", err)
	}
	if _, err := sessionSvc.GetSession(context.Background(), record.ID, attachCaller); err != nil {
		t.Errorf("GetSession(bound attachment) = %v, want success", err)
	}
	if _, err := sessionSvc.GetSession(context.Background(), record.ID, intruderCaller); !errors.Is(err, ErrForbidden) {
		t.Errorf("GetSession(unrelated account) = %v, want ErrForbidden", err)
	}
	if err := sessionSvc.AuthorizeSessionModeration(context.Background(), record.ID, intruderCaller); !errors.Is(err, ErrForbidden) {
		t.Errorf("AuthorizeSessionModeration(unrelated account) = %v, want ErrForbidden", err)
	}
	if err := sessionSvc.AuthorizeSessionModeration(context.Background(), record.ID, ownerCaller); err != nil {
		t.Errorf("AuthorizeSessionModeration(owner) = %v, want success", err)
	}
}

func TestCloseSessionDisconnectsAllAndReturnsTeardownTargets(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	_ = ownerCaller

	closed, teardownIDs, err := sessionSvc.CloseSession(context.Background(), record.ID, testAuditContext())
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if closed.Status != "closed" {
		t.Errorf("CloseSession status = %q, want closed", closed.Status)
	}
	if len(teardownIDs) != 1 || teardownIDs[0] != record.Attachments[0].ID {
		t.Errorf("CloseSession teardownIDs = %v, want [%s]", teardownIDs, record.Attachments[0].ID)
	}
	if closed.Attachments[0].Status != "disconnected" {
		t.Errorf("attachment status after close = %q, want disconnected", closed.Attachments[0].Status)
	}
	if !closed.Attachments[0].TransportTeardownPending {
		t.Errorf("TransportTeardownPending after close = false, want true")
	}

	if err := sessionSvc.CompleteAttachmentTransportTeardown(context.Background(), record.ID, teardownIDs[0]); err != nil {
		t.Fatalf("CompleteAttachmentTransportTeardown: %v", err)
	}
	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Attachments[0].TransportTeardownPending {
		t.Errorf("TransportTeardownPending after CompleteAttachmentTransportTeardown = true, want false")
	}
}

func TestCloseSessionTxCommitsAtomicallyWithCallerWork(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, _ := bootstrapSession(t, sessionSvc, authSvc, db)

	q := repo.New(db)
	closed, teardownIDs, err := sessionSvc.CloseSessionTx(context.Background(), q, record.ID, testAuditContext())
	if err != nil {
		t.Fatalf("CloseSessionTx: %v", err)
	}
	if closed.Status != "closed" {
		t.Errorf("CloseSessionTx status = %q, want closed", closed.Status)
	}
	if len(teardownIDs) != 1 || teardownIDs[0] != record.Attachments[0].ID {
		t.Errorf("CloseSessionTx teardownIDs = %v, want [%s]", teardownIDs, record.Attachments[0].ID)
	}
}

// newTestServicesWithTimeout is newTestServices with a caller-chosen
// owner-absence timeout, for tests that need a deadline they can force into
// the past (or future) relative to a test-supplied "now" without sleeping.
func newTestServicesWithTimeout(t *testing.T, timeout time.Duration) (*Service, *auth.Service, *sql.DB) {
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
	return NewService(db, authSvc, &KeyedMutex{}, timeout), authSvc, db
}

func TestCloseExpiredOwnerAbsenceSessionClosesWhenDeadlinePassed(t *testing.T) {
	sessionSvc, authSvc, db := newTestServicesWithTimeout(t, time.Millisecond)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]

	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, ownerCaller, testAuditContext()); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	future := time.Now().Add(time.Hour)
	closed, teardownIDs, didClose, err := sessionSvc.CloseExpiredOwnerAbsenceSession(context.Background(), record.ID, future, testAuditContext())
	if err != nil {
		t.Fatalf("CloseExpiredOwnerAbsenceSession: %v", err)
	}
	if !didClose {
		t.Fatalf("CloseExpiredOwnerAbsenceSession closed = false, want true")
	}
	if closed.Status != "closed" {
		t.Errorf("status = %q, want closed", closed.Status)
	}
	if len(teardownIDs) != 1 || teardownIDs[0] != attachment.ID {
		t.Errorf("teardownIDs = %v, want [%s]", teardownIDs, attachment.ID)
	}

	// Idempotent: calling again after closure is a no-op, not an error.
	_, _, didCloseAgain, err := sessionSvc.CloseExpiredOwnerAbsenceSession(context.Background(), record.ID, future, testAuditContext())
	if err != nil {
		t.Fatalf("CloseExpiredOwnerAbsenceSession (second call): %v", err)
	}
	if didCloseAgain {
		t.Errorf("CloseExpiredOwnerAbsenceSession second call closed = true, want false (already closed)")
	}
}

func TestCloseExpiredOwnerAbsenceSessionSkipsWhenDeadlineNotYetPassed(t *testing.T) {
	sessionSvc, authSvc, db := newTestServicesWithTimeout(t, time.Hour)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]

	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, ownerCaller, testAuditContext()); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	_, _, didClose, err := sessionSvc.CloseExpiredOwnerAbsenceSession(context.Background(), record.ID, time.Now(), testAuditContext())
	if err != nil {
		t.Fatalf("CloseExpiredOwnerAbsenceSession: %v", err)
	}
	if didClose {
		t.Errorf("CloseExpiredOwnerAbsenceSession closed = true, want false (deadline an hour out)")
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "active" {
		t.Errorf("status after skipped close = %q, want active", after.Status)
	}
}

func TestListExpiredOwnerAbsenceSessionIDs(t *testing.T) {
	sessionSvc, authSvc, db := newTestServicesWithTimeout(t, time.Millisecond)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	attachment := record.Attachments[0]

	if ids, err := sessionSvc.ListExpiredOwnerAbsenceSessionIDs(context.Background(), time.Now()); err != nil {
		t.Fatalf("ListExpiredOwnerAbsenceSessionIDs (before disconnect): %v", err)
	} else if len(ids) != 0 {
		t.Errorf("ListExpiredOwnerAbsenceSessionIDs (before disconnect) = %v, want empty", ids)
	}

	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, attachment.ID, ownerCaller, testAuditContext()); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	ids, err := sessionSvc.ListExpiredOwnerAbsenceSessionIDs(context.Background(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("ListExpiredOwnerAbsenceSessionIDs: %v", err)
	}
	if len(ids) != 1 || ids[0] != record.ID {
		t.Errorf("ListExpiredOwnerAbsenceSessionIDs = %v, want [%s]", ids, record.ID)
	}
}

func TestSetAttachmentPendingTargetRoleAndConfirmAttachmentRole(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, _ := bootstrapSession(t, sessionSvc, authSvc, db)
	// Role transitions never apply to the owner attachment in production
	// (transport.Service.TransitionAttachmentRole rejects it before calling
	// these primitives); add a second, non-owner attachment to exercise them.
	if _, err := db.Exec(
		`INSERT INTO attachments (id, session_id, client_id, user_id, role, status, created_at, updated_at)
		 VALUES ('a2', ?, 'client2', NULL, 'observer', 'connected', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		record.ID,
	); err != nil {
		t.Fatalf("insert second attachment: %v", err)
	}

	target := "participant"
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	q := repo.New(tx)

	pending, err := sessionSvc.SetAttachmentPendingTargetRole(context.Background(), q, record.ID, "a2", &target)
	if err != nil {
		t.Fatalf("SetAttachmentPendingTargetRole: %v", err)
	}
	if pending.PendingTargetRole == nil || *pending.PendingTargetRole != "participant" {
		t.Fatalf("SetAttachmentPendingTargetRole pending role = %v, want participant", pending.PendingTargetRole)
	}
	if pending.EffectiveTransportRole != "observer" {
		t.Errorf("EffectiveTransportRole while pending = %q, want observer (fail-safe)", pending.EffectiveTransportRole)
	}

	confirmed, err := sessionSvc.ConfirmAttachmentRole(context.Background(), q, record.ID, "a2", "participant")
	if err != nil {
		t.Fatalf("ConfirmAttachmentRole: %v", err)
	}
	if confirmed.Role != "participant" || confirmed.PendingTargetRole != nil {
		t.Errorf("ConfirmAttachmentRole result = %+v, want role=participant, pending=nil", confirmed)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
