package adminapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"mako/internal/controlplane/admin"
	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/session"
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fakeWorkerClient backs an httptest.Server standing in for a worker's HTTP
// API 鈥?instance.Service no longer takes a WorkerClient directly (every
// worker interaction resolves a client from a spawned/persisted base URL),
// so this package's tests operate at the HTTP boundary via
// fakePlacementSelector below. Shared by every test file in this package.
type fakeWorkerClient struct {
	templates []runtime.Template

	mu     sync.Mutex
	server *httptest.Server
}

func (f *fakeWorkerClient) httpServer() *httptest.Server {
	if f.server != nil {
		return f.server
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("GET /v1/templates", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(workerapi.TemplateListResponse{Templates: f.templates})
	})
	mux.HandleFunc("POST /v1/instances/start", func(w http.ResponseWriter, r *http.Request) {
		var req workerapi.StartInstanceRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(workerapi.StartInstanceResponse{State: "running", Handle: runtime.InstanceHandle{InstanceID: req.InstanceID}})
	})
	mux.HandleFunc("POST /v1/instances/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(workerapi.StopInstanceResponse{State: "stopped"})
	})
	f.server = httptest.NewServer(mux)
	return f.server
}

// fakePlacementSelector is an instance.PlacementSelector stand-in whose
// AcquireWorker returns worker's httptest.Server address instead of
// spawning a real process.
type fakePlacementSelector struct {
	worker  *fakeWorkerClient
	nextPID int
}

func (s *fakePlacementSelector) Validate(policy placement.Policy) error {
	return (&placement.Selector{}).Validate(policy)
}

func (s *fakePlacementSelector) AcquireWorker(ctx context.Context, instanceID string) (string, int, error) {
	s.nextPID++
	return s.worker.httpServer().URL, s.nextPID, nil
}

func (s *fakePlacementSelector) ReleaseWorker(ctx context.Context, pid int) error { return nil }

// newTestAdminInstanceSetup builds one shared DB backing an *auth.Service,
// an *instance.Service, and an *admin.Service 鈥?instance ownership checks
// (assertCanCreateInstance) look users up by ID, so all three must agree on
// which database those users live in.
func newTestAdminInstanceSetup(t *testing.T) (*auth.Service, *instance.Service, *admin.Service, string) {
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
	secretPath := filepath.Join(t.TempDir(), "bootstrap-secret")
	authSvc, err := auth.NewService(db, secretPath)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator"}}}
	t.Cleanup(func() {
		if worker.server != nil {
			worker.server.Close()
		}
	})
	instanceSvc := instance.NewService(db, &fakePlacementSelector{worker: worker})
	sessionSvc := session.NewService(db, authSvc, &session.KeyedMutex{}, time.Minute)
	adminSvc := admin.NewService(db, authSvc, sessionSvc, instanceSvc)
	return authSvc, instanceSvc, adminSvc, secretPath
}

func createOwnedInstance(t *testing.T, svc *instance.Service, ownerID string) instance.ManagedInstance {
	t.Helper()
	created, err := svc.CreateManagedInstance(context.Background(), instance.CreateInstanceRequest{
		Name:       "owned-instance",
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
	return created
}

func TestAdminInstanceLifecycleCrossOwner(t *testing.T) {
	authSvc, instanceSvc, adminSvc, secretPath := newTestAdminInstanceSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, authSvc, secretPath, "admin1", "correct horse battery staple")

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustAdminUserID(t, authSvc, adminToken), nil, "req-1")
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	owner, _, err := authSvc.Register(context.Background(), inviteToken, "owner1", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	created := createOwnedInstance(t, instanceSvc, owner.ID)

	mux := http.NewServeMux()
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, adminSvc, authSvc)

	req := httptest.NewRequest("POST", "/api/v1/admin/instances/"+created.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin start status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("POST", "/api/v1/admin/instances/"+created.ID+"/stop", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin stop status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest("DELETE", "/api/v1/admin/instances/"+created.ID, nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("admin delete status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminListInstancesSpansOwners(t *testing.T) {
	authSvc, instanceSvc, adminSvc, secretPath := newTestAdminInstanceSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, authSvc, secretPath, "admin1", "correct horse battery staple")

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustAdminUserID(t, authSvc, adminToken), nil, "req-1")
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	owner, _, err := authSvc.Register(context.Background(), inviteToken, "owner1", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	created := createOwnedInstance(t, instanceSvc, owner.ID)

	mux := http.NewServeMux()
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, adminSvc, authSvc)

	req := httptest.NewRequest("GET", "/api/v1/admin/instances", nil)
	req.Header.Set("Authorization", "Bearer "+adminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin list instances status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), created.ID) {
		t.Errorf("admin list instances body = %s, want it to contain %s", rec.Body.String(), created.ID)
	}
}

func TestAdminInstanceRoutesRejectNonAdmin(t *testing.T) {
	authSvc, instanceSvc, adminSvc, secretPath := newTestAdminInstanceSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, authSvc, secretPath, "admin1", "correct horse battery staple")

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustAdminUserID(t, authSvc, adminToken), nil, "req-1")
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	owner, nonAdminToken, err := authSvc.Register(context.Background(), inviteToken, "owner1", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	created := createOwnedInstance(t, instanceSvc, owner.ID)

	mux := http.NewServeMux()
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, adminSvc, authSvc)

	req := httptest.NewRequest("POST", "/api/v1/admin/instances/"+created.ID+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+nonAdminToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func mustAdminUserID(t *testing.T, authSvc *auth.Service, token string) string {
	t.Helper()
	authed, err := authSvc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return authed.UserID
}
