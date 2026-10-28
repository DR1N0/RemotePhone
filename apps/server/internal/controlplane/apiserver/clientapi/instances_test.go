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

	"remotephone/internal/controlplane/apiserver/clientapi"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/instance"
	"remotephone/internal/controlplane/persistence"
	"remotephone/internal/controlplane/placement"
	workerapi "remotephone/internal/worker/api"
	"remotephone/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fakeWorkerClient backs an httptest.Server standing in for a worker's HTTP
// API — route tests exercise the HTTP layer end to end against a real
// instance.Service, but never shell out to a real remotephone-worker process.
// Shared by every test file in this package via fakePlacementSelector.
type fakeWorkerClient struct {
	templates []runtime.Template
	server    *httptest.Server
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
		json.NewEncoder(w).Encode(workerapi.StartInstanceResponse{State: "running", Handle: runtime.InstanceHandle{InstanceID: req.InstanceID, PID: 1}})
	})
	mux.HandleFunc("POST /v1/instances/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(workerapi.StopInstanceResponse{State: "stopped"})
	})
	// Transport/signaling routes — needed by sessions_test.go's
	// promote/demote/disconnect/signaling flows, which drive a real
	// transport.Service against this same fake worker.
	mux.HandleFunc("POST /v1/transport/session", func(w http.ResponseWriter, r *http.Request) {
		var req workerapi.CreateTransportSessionRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(workerapi.TransportSessionResponse{
			SessionID: req.SessionID, AttachmentID: req.AttachmentID, InstanceID: req.InstanceID,
			Mode: "prototype-webrtc", Status: "active", WorkerBaseURL: f.server.URL,
		})
	})
	mux.HandleFunc("PATCH /v1/transport/session/role", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("DELETE /v1/transport/session", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("POST /v1/transport/offer", func(w http.ResponseWriter, r *http.Request) {
		var req workerapi.TransportOfferRequest
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(workerapi.TransportOfferResponse{SdpAnswer: "answer-for-" + req.SdpOffer})
	})
	mux.HandleFunc("POST /v1/transport/ice-candidate", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
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
	t.Cleanup(func() {
		if worker.server != nil {
			worker.server.Close()
		}
	})
	instanceSvc := instance.NewService(db, &fakePlacementSelector{worker: worker})
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

	inviteToken, _, err := authSvc.CreateRegistrationInvite(context.Background(), mustUserID(t, authSvc, ownerToken), nil, "req-1")
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
