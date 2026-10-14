package clientapi_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/apiserver/clientapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fakeWorkerClient is a local, minimal instance.WorkerClient stand-in —
// route tests exercise the HTTP layer end to end against a real
// instance.Service, but never shell out to a real mako-worker process.
type fakeWorkerClient struct {
	templates []runtime.Template
}

func (f *fakeWorkerClient) ListTemplates(ctx context.Context) ([]runtime.Template, error) {
	return f.templates, nil
}
func (f *fakeWorkerClient) StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error) {
	return workerapi.StartInstanceResponse{State: "running", Handle: runtime.InstanceHandle{InstanceID: req.InstanceID, PID: 1}}, nil
}
func (f *fakeWorkerClient) InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error) {
	return workerapi.InspectInstanceResponse{}, nil
}
func (f *fakeWorkerClient) StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error) {
	return workerapi.StopInstanceResponse{State: "stopped"}, nil
}

func newTestInstanceSetup(t *testing.T) (*auth.Service, *instance.Service, string) {
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
	worker := &fakeWorkerClient{templates: []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator", DisplayName: "T1"}}}
	instanceSvc := instance.NewService(db, worker, placement.NewSelector())
	return authSvc, instanceSvc, secretPath
}

func mustCreateAccount(t *testing.T, authSvc *auth.Service, secretPath, username string) string {
	t.Helper()
	status, err := authSvc.GetSetupStatus(context.Background())
	if err != nil {
		t.Fatalf("GetSetupStatus: %v", err)
	}
	if !status {
		secret, err := os.ReadFile(secretPath)
		if err != nil {
			t.Fatalf("read bootstrap secret: %v", err)
		}
		_, token, err := authSvc.SetupAdmin(context.Background(), auth.SetupAdminInput{
			Username: username, Password: "correct horse battery staple", DeviceLabel: "d", BootstrapSecret: string(secret),
		})
		if err != nil {
			t.Fatalf("SetupAdmin: %v", err)
		}
		return token
	}
	_, token, err := authSvc.Login(context.Background(), auth.LoginInput{Username: username, Password: "correct horse battery staple", DeviceLabel: "d"})
	if err != nil {
		t.Fatalf("Login: %v", err)
	}
	return token
}

func createInstanceBody() []byte {
	body, _ := json.Marshal(instance.CreateInstanceRequest{
		Name:       "my-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: instance.ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	})
	return body
}

func TestInstanceCRUDLifecycle(t *testing.T) {
	authSvc, instanceSvc, secretPath := newTestInstanceSetup(t)
	token := mustCreateAccount(t, authSvc, secretPath, "owner1")

	mux := http.NewServeMux()
	clientapi.RegisterInstanceRoutes(mux, instanceSvc, authSvc)

	// Create.
	req := httptest.NewRequest("POST", "/api/v1/instances", bytes.NewReader(createInstanceBody()))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var created struct {
		Instance instance.ManagedInstance `json:"instance"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id := created.Instance.ID

	// List.
	req = httptest.NewRequest("GET", "/api/v1/instances", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200", rec.Code)
	}

	// Start.
	req = httptest.NewRequest("POST", "/api/v1/instances/"+id+"/start", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("start status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Stop.
	req = httptest.NewRequest("POST", "/api/v1/instances/"+id+"/stop", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("stop status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}

	// Delete.
	req = httptest.NewRequest("DELETE", "/api/v1/instances/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}

	// Get after delete -> 404.
	req = httptest.NewRequest("GET", "/api/v1/instances/"+id, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("get after delete status = %d, want 404", rec.Code)
	}
}

func TestCreateInstanceRequiresAuth(t *testing.T) {
	authSvc, instanceSvc, _ := newTestInstanceSetup(t)
	mux := http.NewServeMux()
	clientapi.RegisterInstanceRoutes(mux, instanceSvc, authSvc)

	req := httptest.NewRequest("POST", "/api/v1/instances", bytes.NewReader(createInstanceBody()))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestGetInstanceForbiddenForNonOwner(t *testing.T) {
	authSvc, instanceSvc, secretPath := newTestInstanceSetup(t)
	ownerToken := mustCreateAccount(t, authSvc, secretPath, "owner1")

	mux := http.NewServeMux()
	clientapi.RegisterInstanceRoutes(mux, instanceSvc, authSvc)

	req := httptest.NewRequest("POST", "/api/v1/instances", bytes.NewReader(createInstanceBody()))
	req.Header.Set("Authorization", "Bearer "+ownerToken)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var created struct {
		Instance instance.ManagedInstance `json:"instance"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustUserID(t, authSvc, ownerToken), nil, "req-1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	_, otherToken, err := authSvc.Register(context.Background(), inviteToken, "other_user", "correct horse battery staple", "d", "req-2")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	req = httptest.NewRequest("GET", "/api/v1/instances/"+created.Instance.ID, nil)
	req.Header.Set("Authorization", "Bearer "+otherToken)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func mustUserID(t *testing.T, authSvc *auth.Service, token string) string {
	t.Helper()
	authed, err := authSvc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return authed.UserID
}
