package instance

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/persistence/repo"
	"mako/internal/controlplane/placement"
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fakeWorkerClient is an in-memory WorkerClient stand-in so Service tests
// never shell out to a real mako-worker process.
type fakeWorkerClient struct {
	templates    []runtime.Template
	startResult  workerapi.StartInstanceResponse
	startErr     error
	stopResult   workerapi.StopInstanceResponse
	stopErr      error
	startedCalls []workerapi.StartInstanceRequest
	stoppedIDs   []string
}

func (f *fakeWorkerClient) ListTemplates(ctx context.Context) ([]runtime.Template, error) {
	return f.templates, nil
}

func (f *fakeWorkerClient) StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error) {
	f.startedCalls = append(f.startedCalls, req)
	if f.startErr != nil {
		return workerapi.StartInstanceResponse{}, f.startErr
	}
	return f.startResult, nil
}

func (f *fakeWorkerClient) InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error) {
	return workerapi.InspectInstanceResponse{}, nil
}

func (f *fakeWorkerClient) StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error) {
	f.stoppedIDs = append(f.stoppedIDs, instanceID)
	if f.stopErr != nil {
		return workerapi.StopInstanceResponse{}, f.stopErr
	}
	return f.stopResult, nil
}

func newTestService(t *testing.T, worker *fakeWorkerClient) *Service {
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
	return NewService(db, worker, placement.NewSelector())
}

func createTestUser(t *testing.T, svc *Service, id, userType, status string) {
	t.Helper()
	_, err := svc.q.CreateUser(context.Background(), repo.CreateUserParams{
		ID: id, DisplayName: id, UserType: userType, Status: status,
	})
	if err != nil {
		t.Fatalf("createTestUser: %v", err)
	}
}

func validCreateRequest() CreateInstanceRequest {
	return CreateInstanceRequest{
		Name:       "my-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	}
}

func TestCreateManagedInstanceSuccess(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator", DisplayName: "T1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")

	got, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}
	if got.OwnerID != "u-1" || got.RuntimeClass != "android-studio-emulator" || got.DesiredState != "stopped" {
		t.Errorf("CreateManagedInstance() = %+v, want owner=u-1 runtimeClass=android-studio-emulator desiredState=stopped", got)
	}
}

func TestCreateManagedInstanceRejectsSecondInstanceForSameOwner(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")

	if _, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1"); err != nil {
		t.Fatalf("first CreateManagedInstance: %v", err)
	}
	_, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if !errors.Is(err, ErrOwnerConflict) {
		t.Errorf("second CreateManagedInstance() error = %v, want ErrOwnerConflict", err)
	}
}

func TestCreateManagedInstanceRejectsGuestOwner(t *testing.T) {
	svc := newTestService(t, nil)
	createTestUser(t, svc, "u-guest", "guest", "active")

	_, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-guest")
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("CreateManagedInstance() error = %v, want ErrForbidden", err)
	}
}

func TestCreateManagedInstanceRejectsUnknownTemplate(t *testing.T) {
	svc := newTestService(t, &fakeWorkerClient{})
	createTestUser(t, svc, "u-1", "registered", "active")

	_, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("CreateManagedInstance() error = %v, want ErrInvalidRequest", err)
	}
}

func TestCreateManagedInstanceRejectsUnsupportedPlacement(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")

	req := validCreateRequest()
	req.Placement = placement.Policy{Kind: "best-fit"}
	_, err := svc.CreateManagedInstance(context.Background(), req, "u-1")
	if !errors.Is(err, ErrInvalidRequest) || !errors.Is(err, placement.ErrUnsupported) {
		t.Errorf("CreateManagedInstance() error = %v, want ErrInvalidRequest and placement.ErrUnsupported", err)
	}
}

func TestStartAndStopManagedInstanceLifecycle(t *testing.T) {
	worker := &fakeWorkerClient{
		templates:   []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator"}},
		startResult: workerapi.StartInstanceResponse{State: "running", Handle: runtime.InstanceHandle{PID: 42}},
		stopResult:  workerapi.StopInstanceResponse{State: "stopped"},
	}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")
	created, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	actor := audit.NewContext(strPtr("u-1"), nil, "req-1")
	started, err := svc.StartManagedInstance(context.Background(), created.ID, "u-1", actor)
	if err != nil {
		t.Fatalf("StartManagedInstance: %v", err)
	}
	if started.DesiredState != "running" || started.ObservedState != "running" || started.RuntimeHandle == nil || started.RuntimeHandle.PID != 42 {
		t.Errorf("StartManagedInstance() = %+v, want desiredState=running observedState=running handle.pid=42", started)
	}
	if len(worker.startedCalls) != 1 || worker.startedCalls[0].InstanceID != created.ID {
		t.Errorf("worker.startedCalls = %+v, want one call for %s", worker.startedCalls, created.ID)
	}

	stopped, err := svc.StopManagedInstance(context.Background(), created.ID, "u-1", actor)
	if err != nil {
		t.Fatalf("StopManagedInstance: %v", err)
	}
	if stopped.DesiredState != "stopped" || stopped.ObservedState != "stopped" || stopped.RuntimeHandle != nil {
		t.Errorf("StopManagedInstance() = %+v, want desiredState=stopped observedState=stopped handle=nil", stopped)
	}
	if len(worker.stoppedIDs) != 1 || worker.stoppedIDs[0] != created.ID {
		t.Errorf("worker.stoppedIDs = %v, want one call for %s", worker.stoppedIDs, created.ID)
	}
}

func TestStopManagedInstanceNeverStartedSkipsWorkerCall(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")
	created, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	actor := audit.NewContext(strPtr("u-1"), nil, "req-1")
	stopped, err := svc.StopManagedInstance(context.Background(), created.ID, "u-1", actor)
	if err != nil {
		t.Fatalf("StopManagedInstance: %v", err)
	}
	if stopped.DesiredState != "stopped" {
		t.Errorf("StopManagedInstance() = %+v, want desiredState=stopped", stopped)
	}
	if len(worker.stoppedIDs) != 0 {
		t.Errorf("worker.stoppedIDs = %v, want no worker calls for a never-started instance", worker.stoppedIDs)
	}
}

func TestManageInstanceForbiddenForNonOwner(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")
	createTestUser(t, svc, "u-2", "registered", "active")
	created, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	if _, err := svc.GetManagedInstance(context.Background(), created.ID, "u-2"); !errors.Is(err, ErrForbidden) {
		t.Errorf("GetManagedInstance() error = %v, want ErrForbidden", err)
	}
}

func TestGetManagedInstanceUncheckedSkipsOwnership(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")
	created, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	got, err := svc.GetManagedInstanceUnchecked(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetManagedInstanceUnchecked: %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("GetManagedInstanceUnchecked() = %+v, want id=%s", got, created.ID)
	}
}

func TestAdminManagedInstanceRequiresActiveAdmin(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")
	createTestUser(t, svc, "u-not-admin", "registered", "active")
	created, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	actor := audit.NewContext(strPtr("u-not-admin"), nil, "req-1")
	if err := svc.DeleteAdminManagedInstance(context.Background(), created.ID, "u-not-admin", actor); !errors.Is(err, ErrForbidden) {
		t.Errorf("DeleteAdminManagedInstance() error = %v, want ErrForbidden", err)
	}
}

func TestDeleteAdminManagedInstanceSucceedsForAdmin(t *testing.T) {
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1"}}}
	svc := newTestService(t, worker)
	createTestUser(t, svc, "u-1", "registered", "active")
	createTestUser(t, svc, "u-admin", "admin", "active")
	created, err := svc.CreateManagedInstance(context.Background(), validCreateRequest(), "u-1")
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}

	actor := audit.NewContext(strPtr("u-admin"), nil, "req-1")
	if err := svc.DeleteAdminManagedInstance(context.Background(), created.ID, "u-admin", actor); err != nil {
		t.Fatalf("DeleteAdminManagedInstance: %v", err)
	}
	if _, err := svc.GetManagedInstanceUnchecked(context.Background(), created.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetManagedInstanceUnchecked() after delete error = %v, want ErrNotFound", err)
	}
}

func strPtr(s string) *string { return &s }
