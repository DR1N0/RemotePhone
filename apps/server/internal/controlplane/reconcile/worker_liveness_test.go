package reconcile

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/workerproc"
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fakeWorker backs every spawn a workerproc.FakeExecer makes during these
// tests with one shared handler: template discovery (CreateManagedInstance
// spawns a short-lived discovery worker), start, and adopt (the respawn
// path under test).
type fakeWorker struct {
	templates   []runtime.Template
	startHandle runtime.InstanceHandle
}

func (w *fakeWorker) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(wr http.ResponseWriter, r *http.Request) { wr.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /v1/templates", func(wr http.ResponseWriter, r *http.Request) {
		json.NewEncoder(wr).Encode(workerapi.TemplateListResponse{Templates: w.templates})
	})
	mux.HandleFunc("POST /v1/instances/start", func(wr http.ResponseWriter, r *http.Request) {
		json.NewEncoder(wr).Encode(workerapi.StartInstanceResponse{State: "running", Handle: w.startHandle})
	})
	mux.HandleFunc("POST /v1/instances/{id}/adopt", func(wr http.ResponseWriter, r *http.Request) {
		var req workerapi.AdoptInstanceRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(wr).Encode(workerapi.AdoptInstanceResponse{Handle: req.Handle})
	})
	return mux
}

func testAuditContextForWorkerLiveness() audit.Context {
	return audit.NewContext(nil, nil, "test-request")
}

func newTestWorkerLivenessSetup(t *testing.T) (*WorkerLivenessProbe, *instance.Service, *fakeWorker, *workerproc.FakeExecer, *sql.DB) {
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

	worker := &fakeWorker{}
	execer := &workerproc.FakeExecer{Handler: worker.handler}
	placementSelector := placement.NewSelector(execer, "fake-mako-worker")
	instanceSvc := instance.NewService(db, placementSelector)
	probe := NewWorkerLivenessProbe(instanceSvc, placementSelector)
	return probe, instanceSvc, worker, execer, db
}

func createTestUserForWorkerLiveness(t *testing.T, db *sql.DB, id string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO users (id, display_name, user_type, status, created_at, updated_at)
		 VALUES (?, ?, 'registered', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		id, id,
	); err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
}

func createAndStartTestInstance(t *testing.T, instanceSvc *instance.Service, worker *fakeWorker) instance.ManagedInstance {
	t.Helper()
	worker.templates = []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator"}}
	worker.startHandle = runtime.InstanceHandle{PID: 4242, ConsolePort: 5600, AdbPort: 5601, GRPCPort: 8554}

	created, err := instanceSvc.CreateManagedInstance(context.Background(), instance.CreateInstanceRequest{
		Name:       "my-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: instance.ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	}, "owner")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}
	started, err := instanceSvc.StartManagedInstance(context.Background(), created.ID, "owner", testAuditContextForWorkerLiveness())
	if err != nil {
		t.Fatalf("StartManagedInstance: %v", err)
	}
	if started.RuntimeHandle == nil || started.RuntimeHandle.WorkerPID == 0 {
		t.Fatalf("started instance has no worker recorded: %+v", started)
	}
	return started
}

func TestWorkerLivenessReconcileRespawnsAndAdoptsOnCrash(t *testing.T) {
	probe, instanceSvc, worker, execer, db := newTestWorkerLivenessSetup(t)
	createTestUserForWorkerLiveness(t, db, "owner")
	started := createAndStartTestInstance(t, instanceSvc, worker)
	originalPID := started.RuntimeHandle.WorkerPID

	// No-op while the (only) worker is still healthy.
	if err := probe.Reconcile(context.Background(), started.ID); err != nil {
		t.Fatalf("Reconcile (healthy): %v", err)
	}
	unchanged, err := instanceSvc.GetManagedInstanceUnchecked(context.Background(), started.ID)
	if err != nil {
		t.Fatalf("GetManagedInstanceUnchecked: %v", err)
	}
	if unchanged.RuntimeHandle.WorkerPID != originalPID {
		t.Fatalf("worker PID changed on a healthy no-op reconcile: %d -> %d", originalPID, unchanged.RuntimeHandle.WorkerPID)
	}

	// Simulate a crash: kill the worker process actually spawned for this
	// instance (the LAST spawn — an earlier one was CreateManagedInstance's
	// short-lived, already-released template-discovery worker).
	spawnCountBeforeCrash := len(execer.Processes)
	if spawnCountBeforeCrash == 0 {
		t.Fatal("no worker processes spawned yet")
	}
	if err := execer.Processes[spawnCountBeforeCrash-1].Kill(); err != nil {
		t.Fatalf("kill current worker: %v", err)
	}

	if err := probe.Reconcile(context.Background(), started.ID); err != nil {
		t.Fatalf("Reconcile (after crash): %v", err)
	}

	respawned, err := instanceSvc.GetManagedInstanceUnchecked(context.Background(), started.ID)
	if err != nil {
		t.Fatalf("GetManagedInstanceUnchecked: %v", err)
	}
	if respawned.RuntimeHandle.WorkerPID == originalPID {
		t.Error("worker PID unchanged after a simulated crash, want a respawn")
	}
	if respawned.RuntimeHandle.WorkerBaseURL == "" {
		t.Error("respawned handle has no WorkerBaseURL")
	}
	// The emulator-side fields (PID/ports) must survive the respawn
	// unchanged — that's the whole point of "adopt the still-running
	// emulator," not reboot it.
	if respawned.RuntimeHandle.PID != 4242 || respawned.RuntimeHandle.ConsolePort != 5600 {
		t.Errorf("emulator fields changed across respawn: %+v", respawned.RuntimeHandle)
	}
	if len(execer.Processes) != spawnCountBeforeCrash+1 {
		t.Fatalf("Processes = %d, want %d (one more: the respawn)", len(execer.Processes), spawnCountBeforeCrash+1)
	}

	// Idempotent: reconciling again once healthy is a no-op.
	pidAfterRespawn := respawned.RuntimeHandle.WorkerPID
	if err := probe.Reconcile(context.Background(), started.ID); err != nil {
		t.Fatalf("Reconcile (after respawn, now healthy): %v", err)
	}
	stable, err := instanceSvc.GetManagedInstanceUnchecked(context.Background(), started.ID)
	if err != nil {
		t.Fatalf("GetManagedInstanceUnchecked: %v", err)
	}
	if stable.RuntimeHandle.WorkerPID != pidAfterRespawn {
		t.Error("Reconcile respawned again on an already-healthy worker")
	}
}

func TestWorkerLivenessReconcileNoopsOnNonRunningInstance(t *testing.T) {
	probe, instanceSvc, worker, execer, db := newTestWorkerLivenessSetup(t)
	createTestUserForWorkerLiveness(t, db, "owner")
	worker.templates = []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator"}}

	created, err := instanceSvc.CreateManagedInstance(context.Background(), instance.CreateInstanceRequest{
		Name:       "my-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: instance.ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	}, "owner")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}
	spawnCountAfterCreate := len(execer.Processes)

	// Never started — DesiredState is "stopped", nothing to health-check.
	if err := probe.Reconcile(context.Background(), created.ID); err != nil {
		t.Fatalf("Reconcile (never started): %v", err)
	}
	if len(execer.Processes) != spawnCountAfterCreate {
		t.Errorf("Processes = %d, want %d (reconcile on a never-started instance must not spawn)", len(execer.Processes), spawnCountAfterCreate)
	}
}

func TestWorkerLivenessReconcileNoopsOnUnknownInstance(t *testing.T) {
	probe, _, _, _, _ := newTestWorkerLivenessSetup(t)
	if err := probe.Reconcile(context.Background(), "does-not-exist"); err != nil {
		t.Errorf("Reconcile(unknown id) = %v, want nil", err)
	}
}
