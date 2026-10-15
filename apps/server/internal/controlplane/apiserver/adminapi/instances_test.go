package adminapi_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

type fakeWorkerClient struct{ templates []runtime.Template }

func (f *fakeWorkerClient) ListTemplates(ctx context.Context) ([]runtime.Template, error) {
	return f.templates, nil
}
func (f *fakeWorkerClient) StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error) {
	return workerapi.StartInstanceResponse{State: "running", Handle: runtime.InstanceHandle{InstanceID: req.InstanceID}}, nil
}
func (f *fakeWorkerClient) InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error) {
	return workerapi.InspectInstanceResponse{}, nil
}
func (f *fakeWorkerClient) StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error) {
	return workerapi.StopInstanceResponse{State: "stopped"}, nil
}
func (f *fakeWorkerClient) CreateTransportSession(ctx context.Context, req workerapi.CreateTransportSessionRequest) (workerapi.TransportSessionResponse, error) {
	return workerapi.TransportSessionResponse{}, nil
}
func (f *fakeWorkerClient) UpdateTransportRole(ctx context.Context, req workerapi.UpdateTransportSessionRoleRequest) error {
	return nil
}
func (f *fakeWorkerClient) DestroyTransportSession(ctx context.Context, instanceID, attachmentID string) error {
	return nil
}
func (f *fakeWorkerClient) ForwardOffer(ctx context.Context, req workerapi.TransportOfferRequest) (workerapi.TransportOfferResponse, error) {
	return workerapi.TransportOfferResponse{}, nil
}
func (f *fakeWorkerClient) ForwardICECandidate(ctx context.Context, req workerapi.ICECandidateRequest) error {
	return nil
}

// newTestAdminInstanceSetup builds one shared DB backing BOTH an
// *auth.Service and an *instance.Service — instance ownership checks
// (assertCanCreateInstance) look users up by ID, so the two services must
// agree on which database those users live in.
func newTestAdminInstanceSetup(t *testing.T) (*auth.Service, *instance.Service, string) {
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
	instanceSvc := instance.NewService(db, worker, placement.NewSelector())
	return authSvc, instanceSvc, secretPath
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
	authSvc, instanceSvc, secretPath := newTestAdminInstanceSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, authSvc, secretPath, "admin1", "correct horse battery staple")

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustAdminUserID(t, authSvc, adminToken), nil, "req-1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	owner, _, err := authSvc.Register(context.Background(), inviteToken, "owner1", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	created := createOwnedInstance(t, instanceSvc, owner.ID)

	mux := http.NewServeMux()
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, authSvc)

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

func TestAdminInstanceRoutesRejectNonAdmin(t *testing.T) {
	authSvc, instanceSvc, secretPath := newTestAdminInstanceSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, authSvc, secretPath, "admin1", "correct horse battery staple")

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustAdminUserID(t, authSvc, adminToken), nil, "req-1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	owner, nonAdminToken, err := authSvc.Register(context.Background(), inviteToken, "owner1", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	created := createOwnedInstance(t, instanceSvc, owner.ID)

	mux := http.NewServeMux()
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, authSvc)

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
