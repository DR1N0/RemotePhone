package reconcile

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"

	_ "modernc.org/sqlite"
)

// fakeWorkerServer is a minimal worker HTTP stand-in — these tests only
// need DELETE /v1/transport/session to succeed so
// TeardownPendingAttachment's worker call doesn't fail; nothing else is
// ever invoked for OwnerAbsenceProbe's own tests. Shared with
// worker_liveness_test.go's richer fake where a test needs more routes.
func newFakeWorkerServer() *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("DELETE /v1/transport/session", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	return httptest.NewServer(mux)
}

// fakePlacementSelector is an instance.PlacementSelector stand-in whose
// AcquireWorker always points at one pre-built fake worker server, instead
// of spawning a real process — sufficient for OwnerAbsenceProbe's tests,
// which never exercise the respawn path (that's WorkerLivenessProbe's own
// test file).
type fakePlacementSelector struct {
	baseURL string
	nextPID int
}

func (s *fakePlacementSelector) Validate(policy placement.Policy) error {
	return (&placement.Selector{}).Validate(policy)
}

func (s *fakePlacementSelector) AcquireWorker(ctx context.Context, instanceID string) (string, int, error) {
	s.nextPID++
	return s.baseURL, s.nextPID, nil
}

func (s *fakePlacementSelector) ReleaseWorker(ctx context.Context, pid int) error { return nil }

func newTestProbe(t *testing.T, ownerAbsenceTimeout time.Duration) (*OwnerAbsenceProbe, *session.Service, *auth.Service, *sql.DB, string) {
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
	server := newFakeWorkerServer()
	t.Cleanup(server.Close)
	placementSelector := &fakePlacementSelector{baseURL: server.URL}
	instanceSvc := instance.NewService(db, placementSelector)
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, authSvc, keyedMutex, ownerAbsenceTimeout)
	transportSvc := transport.NewService(db, sessionSvc, instanceSvc, keyedMutex)
	return NewOwnerAbsenceProbe(sessionSvc, transportSvc), sessionSvc, authSvc, db, server.URL
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

// createTestInstance inserts a "running" instance row with a runtime handle
// already pointing at workerBaseURL — OwnerAbsenceProbe's own tests never
// start an instance through instance.Service (session creation just needs
// an existing instance row to reference), but transport.Service's
// TeardownPendingAttachment resolves a WorkerClient from this persisted
// handle, so one must be present.
func createTestInstance(t *testing.T, db *sql.DB, id, ownerID, workerBaseURL string) {
	t.Helper()
	handleJSON := `{"instanceId":"` + id + `","runtimeClass":"android-studio-emulator","workerId":"local","workerBaseUrl":"` + workerBaseURL + `"}`
	if _, err := db.Exec(
		`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state, runtime_handle_json, created_at, updated_at)
		 VALUES (?, ?, 'test-instance', 'android-studio-emulator', 't1', '{"kind":"local-only"}', '{}', '{}', 'running', 'running', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, ownerID, handleJSON,
	); err != nil {
		t.Fatalf("createTestInstance: %v", err)
	}
}

func TestReconcileClosesSessionPastDeadlineAndTearsDownTransport(t *testing.T) {
	probe, sessionSvc, authSvc, db, workerBaseURL := newTestProbe(t, time.Millisecond)
	probe.now = func() time.Time { return time.Now().Add(time.Hour) }

	createTestUser(t, db, "owner")
	createTestInstance(t, db, "i1", "owner", workerBaseURL)
	token, err := authSvc.IssueAccountCredential(context.Background(), "owner", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	ownerCaller, err := authSvc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	record, _, err := sessionSvc.CreateSession(context.Background(), "i1", "owner-client", nil, *ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	actor := audit.NewContext(nil, nil, "test-request")
	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, record.Attachments[0].ID, *ownerCaller, actor); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	if err := probe.Reconcile(context.Background(), record.ID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "closed" {
		t.Errorf("status after Reconcile = %q, want closed", after.Status)
	}
	if after.Attachments[0].TransportTeardownPending {
		t.Errorf("TransportTeardownPending after Reconcile = true, want false (torn down)")
	}

	// Idempotent: a second Reconcile call after closure is a no-op.
	if err := probe.Reconcile(context.Background(), record.ID); err != nil {
		t.Errorf("Reconcile (second call): %v", err)
	}
}

func TestReconcileSkipsSessionNotYetPastDeadline(t *testing.T) {
	probe, sessionSvc, authSvc, db, workerBaseURL := newTestProbe(t, time.Hour)

	createTestUser(t, db, "owner")
	createTestInstance(t, db, "i1", "owner", workerBaseURL)
	token, err := authSvc.IssueAccountCredential(context.Background(), "owner", "device")
	if err != nil {
		t.Fatalf("IssueAccountCredential: %v", err)
	}
	ownerCaller, err := authSvc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	record, _, err := sessionSvc.CreateSession(context.Background(), "i1", "owner-client", nil, *ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	actor := audit.NewContext(nil, nil, "test-request")
	if _, err := sessionSvc.DisconnectAttachment(context.Background(), record.ID, record.Attachments[0].ID, *ownerCaller, actor); err != nil {
		t.Fatalf("DisconnectAttachment: %v", err)
	}

	if err := probe.Reconcile(context.Background(), record.ID); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	after, err := sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "active" {
		t.Errorf("status after Reconcile (deadline an hour out) = %q, want still active", after.Status)
	}
}
