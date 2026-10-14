package session

import (
	"context"
	"errors"
	"testing"
)

func TestCreateAndRedeemSessionInviteAsGuest(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)

	pin, expiresAt, err := sessionSvc.CreateSessionInvite(context.Background(), record.ID, ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("CreateSessionInvite: %v", err)
	}
	if len(pin) != 6 {
		t.Errorf("CreateSessionInvite pin = %q, want 6 digits", pin)
	}
	if expiresAt == "" {
		t.Errorf("CreateSessionInvite expiresAt = %q, want non-empty", expiresAt)
	}

	name := "A Friend"
	attachment, token, user, err := sessionSvc.RedeemSessionInvite(context.Background(), pin, SessionInviteModeGuest, nil, &name, nil, "req-1")
	if err != nil {
		t.Fatalf("RedeemSessionInvite: %v", err)
	}
	if attachment.Role != "observer" || attachment.SessionID != record.ID {
		t.Errorf("RedeemSessionInvite attachment = %+v, want observer attached to %s", attachment, record.ID)
	}
	if user.Type != "guest" || user.DisplayName != name {
		t.Errorf("RedeemSessionInvite user = %+v, want guest %q", user, name)
	}

	authed := mustAuthenticate(t, authSvc, token)
	if authed.Scope != "attachment" || authed.SessionID == nil || *authed.SessionID != record.ID {
		t.Errorf("Authenticate(token) = %+v, want attachment scope on session %s", authed, record.ID)
	}
}

func TestRedeemSessionInviteIsSingleUse(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	pin, _, err := sessionSvc.CreateSessionInvite(context.Background(), record.ID, ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("CreateSessionInvite: %v", err)
	}

	if _, _, _, err := sessionSvc.RedeemSessionInvite(context.Background(), pin, SessionInviteModeGuest, nil, nil, nil, "req-1"); err != nil {
		t.Fatalf("first RedeemSessionInvite: %v", err)
	}
	_, _, _, err = sessionSvc.RedeemSessionInvite(context.Background(), pin, SessionInviteModeGuest, nil, nil, nil, "req-2")
	if !errors.Is(err, ErrSessionInviteInvalid) {
		t.Fatalf("second RedeemSessionInvite = %v, want ErrSessionInviteInvalid", err)
	}
}

func TestRedeemSessionInviteRejectsUnknownPin(t *testing.T) {
	sessionSvc, _, _ := newTestServices(t)
	_, _, _, err := sessionSvc.RedeemSessionInvite(context.Background(), "000000", SessionInviteModeGuest, nil, nil, nil, "req-1")
	if !errors.Is(err, ErrSessionInviteInvalid) {
		t.Fatalf("RedeemSessionInvite(unknown pin) = %v, want ErrSessionInviteInvalid", err)
	}
}

func TestRedeemSessionInviteAsRegisteredUser(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	pin, _, err := sessionSvc.CreateSessionInvite(context.Background(), record.ID, ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("CreateSessionInvite: %v", err)
	}
	createTestUser(t, db, "friend")

	attachment, _, user, err := sessionSvc.RedeemSessionInvite(context.Background(), pin, SessionInviteModeRegistered, strPtr("friend"), nil, strPtr("cred-1"), "req-1")
	if err != nil {
		t.Fatalf("RedeemSessionInvite: %v", err)
	}
	if user.ID != "friend" || user.Type != "registered" {
		t.Errorf("RedeemSessionInvite user = %+v, want registered user friend", user)
	}
	if attachment.Role != "observer" {
		t.Errorf("RedeemSessionInvite attachment role = %q, want observer", attachment.Role)
	}
}

func TestRedeemSessionInviteRejectsGuestAsRegisteredRequester(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, ownerCaller := bootstrapSession(t, sessionSvc, authSvc, db)
	pin, _, err := sessionSvc.CreateSessionInvite(context.Background(), record.ID, ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("CreateSessionInvite: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO users (id, display_name, user_type, status, created_at, updated_at)
		 VALUES ('ghost', 'ghost', 'guest', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("insert guest user: %v", err)
	}

	_, _, _, err = sessionSvc.RedeemSessionInvite(context.Background(), pin, SessionInviteModeRegistered, strPtr("ghost"), nil, nil, "req-1")
	if !errors.Is(err, ErrSessionInviteInvalid) {
		t.Fatalf("RedeemSessionInvite(guest as registered) = %v, want ErrSessionInviteInvalid", err)
	}
}

func TestCreateSessionInviteRejectsNonOwner(t *testing.T) {
	sessionSvc, authSvc, db := newTestServices(t)
	record, _, _ := bootstrapSession(t, sessionSvc, authSvc, db)
	createTestUser(t, db, "intruder")
	intruderToken, err := authSvc.IssueAccountCredential(context.Background(), "intruder", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	intruderCaller := mustAuthenticate(t, authSvc, intruderToken)

	_, _, err = sessionSvc.CreateSessionInvite(context.Background(), record.ID, intruderCaller, testAuditContext())
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("CreateSessionInvite(non-owner) = %v, want ErrForbidden", err)
	}
}

func strPtr(s string) *string { return &s }
