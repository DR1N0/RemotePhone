package transport

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
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fakeWorkerClient implements instance.WorkerClient so transport.Service
// tests never shell out to a real mako-worker process. Instance-lifecycle
// methods have fixed canned results (tests here only need a running
// instance as a precondition); transport-session methods record calls and
// support error injection, since that's what this package's tests exercise.
type fakeWorkerClient struct {
	createResult workerapi.TransportSessionResponse
	createErr    error
	updateErr    error
	destroyErr   error

	createCalls []workerapi.CreateTransportSessionRequest
	updateCalls []workerapi.UpdateTransportSessionRoleRequest
	destroyIDs  []string
}

func (f *fakeWorkerClient) ListTemplates(ctx context.Context) ([]runtime.Template, error) {
	return []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator"}}, nil
}

func (f *fakeWorkerClient) StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error) {
	return workerapi.StartInstanceResponse{State: "running", Handle: runtime.InstanceHandle{PID: 42}}, nil
}

func (f *fakeWorkerClient) InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error) {
	return workerapi.InspectInstanceResponse{}, nil
}

func (f *fakeWorkerClient) StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error) {
	return workerapi.StopInstanceResponse{State: "stopped"}, nil
}

func (f *fakeWorkerClient) CreateTransportSession(ctx context.Context, req workerapi.CreateTransportSessionRequest) (workerapi.TransportSessionResponse, error) {
	f.createCalls = append(f.createCalls, req)
	if f.createErr != nil {
		return workerapi.TransportSessionResponse{}, f.createErr
	}
	return f.createResult, nil
}

func (f *fakeWorkerClient) UpdateTransportRole(ctx context.Context, req workerapi.UpdateTransportSessionRoleRequest) error {
	f.updateCalls = append(f.updateCalls, req)
	return f.updateErr
}

func (f *fakeWorkerClient) DestroyTransportSession(ctx context.Context, instanceID, attachmentID string) error {
	f.destroyIDs = append(f.destroyIDs, instanceID+":"+attachmentID)
	return f.destroyErr
}

func (f *fakeWorkerClient) ForwardOffer(ctx context.Context, req workerapi.TransportOfferRequest) (workerapi.TransportOfferResponse, error) {
	return workerapi.TransportOfferResponse{SdpAnswer: "answer-for-" + req.SdpOffer}, nil
}

func (f *fakeWorkerClient) ForwardICECandidate(ctx context.Context, req workerapi.ICECandidateRequest) error {
	return nil
}

func newTestServices(t *testing.T, worker *fakeWorkerClient) (*Service, *session.Service, *instance.Service, *auth.Service, *sql.DB) {
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
	if worker == nil {
		worker = &fakeWorkerClient{}
	}

	authSvc, err := auth.NewService(db, "")
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	instanceSvc := instance.NewService(db, worker, placement.NewSelector())
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, authSvc, keyedMutex, time.Minute)
	transportSvc := NewService(db, sessionSvc, instanceSvc, worker, keyedMutex)
	return transportSvc, sessionSvc, instanceSvc, authSvc, db
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

// bootstrapRunningSession creates an owner user + a started instance (so
// RuntimeHandle is populated) + an active session with its owner
// attachment, then redeems a guest invite to add a second, non-owner
// "observer" attachment to exercise transport/role-transition methods
// against. Returns the session record (freshest view, after the guest
// joined) and the two attachment IDs.
func bootstrapRunningSession(t *testing.T, db *sql.DB, sessionSvc *session.Service, instanceSvc *instance.Service, authSvc *auth.Service) (record session.SessionRecord, ownerAttachmentID, guestAttachmentID string, ownerCaller auth.AuthenticatedUser) {
	t.Helper()
	ctx := context.Background()

	ownerID := "owner"
	createTestUser(t, db, ownerID)
	ownerToken, err := authSvc.IssueAccountCredential(ctx, ownerID, "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	authed, err := authSvc.Authenticate(ctx, ownerToken)
	if err != nil || authed == nil {
		t.Fatalf("Authenticate(owner): %+v, %v", authed, err)
	}
	ownerCaller = *authed

	created, err := instanceSvc.CreateManagedInstance(ctx, instance.CreateInstanceRequest{
		Name:       "my-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: instance.ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	}, ownerID)
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}
	if _, err := instanceSvc.StartManagedInstance(ctx, created.ID, ownerID, testAuditContext()); err != nil {
		t.Fatalf("StartManagedInstance: %v", err)
	}

	sessionRecord, _, err := sessionSvc.CreateSession(ctx, created.ID, "owner-client", nil, ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	pin, _, err := sessionSvc.CreateSessionInvite(ctx, sessionRecord.ID, ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("CreateSessionInvite: %v", err)
	}
	guestAttachment, _, _, err := sessionSvc.RedeemSessionInvite(ctx, pin, session.SessionInviteModeGuest, nil, nil, nil, "req-redeem")
	if err != nil {
		t.Fatalf("RedeemSessionInvite: %v", err)
	}

	final, err := sessionSvc.GetSessionUnchecked(ctx, sessionRecord.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	return final, sessionRecord.Attachments[0].ID, guestAttachment.ID, ownerCaller
}

func testAuditContext() audit.Context { return audit.NewContext(nil, nil, "test-request") }

func TestEnsureAttachmentTransportSuccess(t *testing.T) {
	worker := &fakeWorkerClient{createResult: workerapi.TransportSessionResponse{SessionToken: "tok-1", Mode: "webrtc", Status: "ready"}}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, ownerAttachmentID, _, _ := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)

	resp, err := transportSvc.EnsureAttachmentTransport(context.Background(), record.ID, ownerAttachmentID, testAuditContext())
	if err != nil {
		t.Fatalf("EnsureAttachmentTransport: %v", err)
	}
	if resp.SessionToken != "tok-1" {
		t.Errorf("EnsureAttachmentTransport() = %+v, want sessionToken=tok-1", resp)
	}
	if len(worker.createCalls) != 1 || worker.createCalls[0].Role != "owner" {
		t.Errorf("CreateTransportSession calls = %+v, want one call with role=owner", worker.createCalls)
	}
}

func TestEnsureAttachmentTransportFailsWhenInstanceNotRunning(t *testing.T) {
	worker := &fakeWorkerClient{}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, ownerAttachmentID, _, ownerCaller := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)
	if _, err := instanceSvc.StopManagedInstance(context.Background(), record.InstanceID, ownerCaller.UserID, testAuditContext()); err != nil {
		t.Fatalf("StopManagedInstance: %v", err)
	}

	_, err := transportSvc.EnsureAttachmentTransport(context.Background(), record.ID, ownerAttachmentID, testAuditContext())
	if !errors.Is(err, session.ErrInvalidRequest) {
		t.Fatalf("EnsureAttachmentTransport(stopped instance) = %v, want ErrInvalidRequest", err)
	}
}

func TestEnsureAttachmentTransportWrapsWorkerFailure(t *testing.T) {
	worker := &fakeWorkerClient{createErr: errors.New("boom")}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, ownerAttachmentID, _, _ := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)

	_, err := transportSvc.EnsureAttachmentTransport(context.Background(), record.ID, ownerAttachmentID, testAuditContext())
	var unavailable *ErrTransportUnavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("EnsureAttachmentTransport(worker error) = %v, want ErrTransportUnavailable", err)
	}
}

func TestDestroyAndTeardownPendingAttachment(t *testing.T) {
	worker := &fakeWorkerClient{}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, ownerAttachmentID, _, ownerCaller := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)

	if err := transportSvc.DestroyAttachmentTransport(context.Background(), record.ID, ownerAttachmentID, testAuditContext()); err != nil {
		t.Fatalf("DestroyAttachmentTransport: %v", err)
	}
	if len(worker.destroyIDs) != 1 {
		t.Fatalf("DestroyTransportSession calls = %v, want exactly one", worker.destroyIDs)
	}

	// Disconnecting marks transport-teardown-pending; TeardownPendingAttachment
	// should then perform a second destroy and clear the flag.
	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, ownerAttachmentID, ownerCaller, testAuditContext()); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}
	didTeardown, err := transportSvc.TeardownPendingAttachment(context.Background(), record.ID, ownerAttachmentID, testAuditContext())
	if err != nil {
		t.Fatalf("TeardownPendingAttachment: %v", err)
	}
	if !didTeardown {
		t.Errorf("TeardownPendingAttachment() = false, want true")
	}
	if len(worker.destroyIDs) != 2 {
		t.Fatalf("DestroyTransportSession calls = %v, want exactly two", worker.destroyIDs)
	}

	again, err := transportSvc.TeardownPendingAttachment(context.Background(), record.ID, ownerAttachmentID, testAuditContext())
	if err != nil {
		t.Fatalf("TeardownPendingAttachment (second call): %v", err)
	}
	if again {
		t.Errorf("TeardownPendingAttachment() second call = true, want false (already cleared)")
	}
}

func TestTransitionAttachmentRolePromotesGuestToParticipant(t *testing.T) {
	worker := &fakeWorkerClient{}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, _, guestAttachmentID, ownerCaller := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)

	result, err := transportSvc.TransitionAttachmentRole(context.Background(), record.ID, guestAttachmentID, "participant", ownerCaller, testAuditContext())
	if err != nil {
		t.Fatalf("TransitionAttachmentRole: %v", err)
	}
	if result.Role != "participant" || result.PendingTargetRole != nil {
		t.Errorf("TransitionAttachmentRole() = %+v, want role=participant pending=nil", result)
	}
	if len(worker.updateCalls) != 1 || worker.updateCalls[0].Role != "participant" {
		t.Errorf("UpdateTransportRole calls = %+v, want exactly one call with role=participant", worker.updateCalls)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	var found bool
	for _, a := range after.Attachments {
		if a.ID == guestAttachmentID {
			found = true
			if a.Role != "participant" {
				t.Errorf("attachment role = %q, want participant", a.Role)
			}
		}
	}
	if !found {
		t.Fatalf("guest attachment %s not found after transition", guestAttachmentID)
	}
}

func TestTransitionAttachmentRoleRejectsOwnerAttachment(t *testing.T) {
	worker := &fakeWorkerClient{}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, ownerAttachmentID, _, ownerCaller := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)

	_, err := transportSvc.TransitionAttachmentRole(context.Background(), record.ID, ownerAttachmentID, "observer", ownerCaller, testAuditContext())
	if !errors.Is(err, session.ErrInvalidRequest) {
		t.Fatalf("TransitionAttachmentRole(owner attachment) = %v, want ErrInvalidRequest", err)
	}
	if len(worker.updateCalls) != 0 {
		t.Errorf("UpdateTransportRole calls = %v, want none — rejected before touching the worker", worker.updateCalls)
	}
}

func TestTransitionAttachmentRoleRejectsNonOwnerCaller(t *testing.T) {
	worker := &fakeWorkerClient{}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, _, guestAttachmentID, _ := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)
	createTestUser(t, db, "intruder")
	intruderToken, err := authSvc.IssueAccountCredential(context.Background(), "intruder", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	intruderCaller, err := authSvc.Authenticate(context.Background(), intruderToken)
	if err != nil || intruderCaller == nil {
		t.Fatalf("Authenticate(intruder): %+v, %v", intruderCaller, err)
	}

	_, err = transportSvc.TransitionAttachmentRole(context.Background(), record.ID, guestAttachmentID, "participant", *intruderCaller, testAuditContext())
	if !errors.Is(err, session.ErrForbidden) {
		t.Fatalf("TransitionAttachmentRole(non-owner caller) = %v, want ErrForbidden", err)
	}
}

// TestTransitionAttachmentRoleLeavesPendingSetOnWorkerFailure is the core
// regression test for the promote/demote redesign: on a worker failure,
// pending_target_role must stay set (never cleared) and role must stay
// unchanged — closing the TS route-layer race where the DB briefly reported
// a role the worker was never told to enforce.
func TestTransitionAttachmentRoleLeavesPendingSetOnWorkerFailure(t *testing.T) {
	worker := &fakeWorkerClient{updateErr: errors.New("worker unreachable")}
	transportSvc, sessionSvc, instanceSvc, authSvc, db := newTestServices(t, worker)
	record, _, guestAttachmentID, ownerCaller := bootstrapRunningSession(t, db, sessionSvc, instanceSvc, authSvc)

	_, err := transportSvc.TransitionAttachmentRole(context.Background(), record.ID, guestAttachmentID, "participant", ownerCaller, testAuditContext())
	var unavailable *ErrTransportUnavailable
	if !errors.As(err, &unavailable) {
		t.Fatalf("TransitionAttachmentRole(worker failure) = %v, want ErrTransportUnavailable", err)
	}
	// Step 3's real call plus step 5's best-effort force-observer call.
	if len(worker.updateCalls) != 2 || worker.updateCalls[1].Role != "observer" {
		t.Errorf("UpdateTransportRole calls = %+v, want [participant, observer]", worker.updateCalls)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	for _, a := range after.Attachments {
		if a.ID != guestAttachmentID {
			continue
		}
		if a.Role != "observer" {
			t.Errorf("attachment role = %q, want unchanged (observer)", a.Role)
		}
		if a.PendingTargetRole == nil || *a.PendingTargetRole != "participant" {
			t.Errorf("attachment pendingTargetRole = %v, want \"participant\" (left set, never cleared on failure)", a.PendingTargetRole)
		}
		if a.EffectiveTransportRole != "observer" {
			t.Errorf("EffectiveTransportRole = %q, want observer (fail-safe while pending)", a.EffectiveTransportRole)
		}
	}
}

func TestRelayOfferAndICECandidate(t *testing.T) {
	worker := &fakeWorkerClient{}
	transportSvc, _, _, _, _ := newTestServices(t, worker)

	answer, err := transportSvc.RelayOffer(context.Background(), "inst-1", "att-1", "offer-sdp")
	if err != nil {
		t.Fatalf("RelayOffer: %v", err)
	}
	if answer != "answer-for-offer-sdp" {
		t.Errorf("RelayOffer() = %q, want answer-for-offer-sdp", answer)
	}
	if err := transportSvc.RelayICECandidate(context.Background(), "inst-1", "att-1", "candidate-1"); err != nil {
		t.Fatalf("RelayICECandidate: %v", err)
	}
}
