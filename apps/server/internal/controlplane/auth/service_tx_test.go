package auth

import (
	"context"
	"testing"
	"time"

	"remotephone/internal/controlplane/persistence/repo"
)

// TestIssueAttachmentCredentialTxSeesUncommittedRows is the reason
// IssueAttachmentCredentialTx exists: the plain IssueAttachmentCredential
// (bound to s.q, the top-level *sql.DB) cannot see a session/attachment
// inserted earlier in the SAME still-open transaction — and would in fact
// deadlock waiting for a second connection from a pool capped at size 1
// (see NewService). The Tx variant, given the transaction's own
// *repo.Queries, must succeed here.
func TestIssueAttachmentCredentialTxSeesUncommittedRows(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")

	tx, err := svc.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	q := repo.New(tx)
	ctx := context.Background()

	if _, err := tx.Exec(
		`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state, created_at, updated_at)
		 VALUES ('i1', 'u1', 'test-instance', 'android-studio-emulator', 't1', '{"kind":"local-only"}', '{}', '{}', 'stopped', 'stopped', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("insert instance: %v", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO sessions (id, instance_id, status, created_at, updated_at) VALUES ('s1', 'i1', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO attachments (id, session_id, user_id, client_id, role, status, created_at, updated_at)
		 VALUES ('a1', 's1', 'u1', 'client1', 'owner', 'connected', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("insert attachment: %v", err)
	}

	token, err := svc.IssueAttachmentCredentialTx(ctx, q, "u1", "s1", "a1", svc.now().Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueAttachmentCredentialTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	authed, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if authed.Scope != "attachment" || authed.SessionID == nil || *authed.SessionID != "s1" {
		t.Errorf("Authenticate() = %+v, want attachment-scoped credential for session s1", authed)
	}
}

// TestRotateAttachmentCredentialTxSeesUncommittedRows is the same proof for
// the rotate path session.Service.ResumeAttachment needs.
func TestRotateAttachmentCredentialTxSeesUncommittedRows(t *testing.T) {
	svc := newTestService(t)
	createTestUser(t, svc, "u1", "registered", "active")
	createTestSessionAndAttachment(t, svc, "u1", "s1", "a1", "u1")

	original, err := svc.IssueAttachmentCredential(context.Background(), "u1", "s1", "a1", svc.now().Add(time.Hour))
	if err != nil {
		t.Fatalf("IssueAttachmentCredential: %v", err)
	}
	authed, err := svc.Authenticate(context.Background(), original)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}

	tx, err := svc.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	defer tx.Rollback()
	q := repo.New(tx)

	// An uncommitted status change on the SAME transaction the rotate call
	// participates in — proving the rotate call isn't silently reading via
	// a different connection.
	if _, err := tx.Exec(`UPDATE attachments SET status = 'connected' WHERE id = 'a1'`); err != nil {
		t.Fatalf("update attachment: %v", err)
	}

	rotated, err := svc.RotateAttachmentCredentialTx(context.Background(), q, authed.CredentialID, "u1", "s1", "a1")
	if err != nil {
		t.Fatalf("RotateAttachmentCredentialTx: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if authedAfter, err := svc.Authenticate(context.Background(), original); err != nil || authedAfter != nil {
		t.Errorf("Authenticate(original) after rotation = (%+v, %v), want (nil, nil) — revoked", authedAfter, err)
	}
	reauthed, err := svc.Authenticate(context.Background(), rotated)
	if err != nil {
		t.Fatalf("Authenticate(rotated): %v", err)
	}
	if reauthed.Scope != "attachment" {
		t.Errorf("Authenticate(rotated).Scope = %q, want attachment", reauthed.Scope)
	}
}
