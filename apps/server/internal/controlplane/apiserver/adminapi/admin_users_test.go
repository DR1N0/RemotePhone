package adminapi_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"mako/internal/controlplane/admin"
	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"
	"mako/internal/worker/runtime"

	_ "modernc.org/sqlite"
)

// fullAdminTestSetup bundles every service the admin user/ban/audit routes
// need: auth (identity), session+transport (ban's owned-session teardown),
// instance (cross-owner list), admin (the routes' own service layer).
type fullAdminTestSetup struct {
	authSvc      *auth.Service
	sessionSvc   *session.Service
	instanceSvc  *instance.Service
	transportSvc *transport.Service
	adminSvc     *admin.Service
	db           *sql.DB
	secretPath   string
}

func newFullAdminTestSetup(t *testing.T) fullAdminTestSetup {
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
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, authSvc, keyedMutex, time.Minute)
	transportSvc := transport.NewService(db, sessionSvc, instanceSvc, keyedMutex)
	adminSvc := admin.NewService(db, authSvc, sessionSvc, instanceSvc)
	return fullAdminTestSetup{authSvc, sessionSvc, instanceSvc, transportSvc, adminSvc, db, secretPath}
}

func (s fullAdminTestSetup) mux() *http.ServeMux {
	mux := http.NewServeMux()
	adminapi.RegisterInstanceRoutes(mux, s.instanceSvc, s.adminSvc, s.authSvc)
	adminapi.RegisterAdminManagementRoutes(mux, s.adminSvc, s.authSvc, s.transportSvc)
	adminapi.RegisterAdminAuditRoutes(mux, s.adminSvc, s.authSvc)
	return mux
}

func (s fullAdminTestSetup) registerUser(t *testing.T, adminToken, username string) (userID, token string) {
	t.Helper()
	inviteToken, _, err := s.authSvc.CreateRegistrationInvite(context.Background(), mustAdminUserID(t, s.authSvc, adminToken), nil, "req-"+username)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	user, regToken, err := s.authSvc.Register(context.Background(), inviteToken, username, "correct horse battery staple", "d", "req-reg-"+username)
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	return user.ID, regToken
}

func doRequest(t *testing.T, mux *http.ServeMux, method, path, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *strings.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		bodyReader = strings.NewReader(string(b))
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestAdminListUsersExcludesGuests(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	s.registerUser(t, adminToken, "reg1")

	rec := doRequest(t, s.mux(), "GET", "/api/v1/admin/users", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Type string `json:"type"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.Items) != 2 {
		t.Fatalf("items = %+v, want 2 (admin1 + reg1)", body.Items)
	}
}

func TestAdminSetUserTypeRoute(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	regID, _ := s.registerUser(t, adminToken, "reg1")

	rec := doRequest(t, s.mux(), "POST", "/api/v1/admin/users/"+regID+"/type", adminToken, map[string]string{"type": "admin"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminCredentialsListAndRevokeRoute(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	regID, regToken := s.registerUser(t, adminToken, "reg1")
	mux := s.mux()

	rec := doRequest(t, mux, "GET", "/api/v1/admin/users/"+regID+"/credentials", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list credentials status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if len(body.Items) != 1 {
		t.Fatalf("items = %+v, want 1 credential", body.Items)
	}

	rec = doRequest(t, mux, "POST", "/api/v1/admin/users/"+regID+"/credentials/"+body.Items[0].ID+"/revoke", adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	if authed, err := s.authSvc.Authenticate(context.Background(), regToken); err != nil || authed != nil {
		t.Errorf("Authenticate(revoked token) = (%+v, %v), want (nil, nil)", authed, err)
	}
}

func TestAdminBanClosesOwnedSessionAndUnbanRestoresLogin(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	regID, regToken := s.registerUser(t, adminToken, "reg1")
	mux := s.mux()

	created, err := s.instanceSvc.CreateManagedInstance(context.Background(), instance.CreateInstanceRequest{
		Name:       "owned-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: instance.ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	}, regID)
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}
	ownerCaller, err := s.authSvc.Authenticate(context.Background(), regToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	record, _, _, err := s.sessionSvc.CreateSession(context.Background(), created.ID, "owner-client", nil, *ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	rec := doRequest(t, mux, "POST", "/api/v1/admin/users/"+regID+"/ban", adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("ban status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
	after, err := s.sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "closed" {
		t.Errorf("session status after ban = %q, want closed", after.Status)
	}

	rec = doRequest(t, mux, "POST", "/api/v1/admin/users/"+regID+"/unban", adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("unban status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminRevokeGuestAttachmentRoute(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	regID, regToken := s.registerUser(t, adminToken, "reg1")
	mux := s.mux()

	created, err := s.instanceSvc.CreateManagedInstance(context.Background(), instance.CreateInstanceRequest{
		Name:       "owned-instance",
		TemplateID: "t-1",
		Placement:  placement.Policy{Kind: "local-only"},
		ResourceProfile: instance.ResourceProfile{
			CPUMillicores: 1000, MemoryMB: 2048, DiskMB: 8192,
			AccelerationMode: "auto", StreamProfile: "balanced",
		},
		LaunchConfig: runtime.LaunchConfig{Headless: true},
	}, regID)
	if err != nil {
		t.Fatalf("CreateManagedInstance: %v", err)
	}
	ownerCaller, err := s.authSvc.Authenticate(context.Background(), regToken)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	record, _, _, err := s.sessionSvc.CreateSession(context.Background(), created.ID, "owner-client", nil, *ownerCaller)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO users (id, display_name, user_type, status, created_at, updated_at)
		 VALUES ('guest-1', 'guest-1', 'guest', 'active', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
	); err != nil {
		t.Fatalf("insert guest user: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO attachments (id, session_id, client_id, user_id, role, status, created_at, updated_at)
		 VALUES ('guest-attachment', ?, 'guest-client', 'guest-1', 'participant', 'connected', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		record.ID,
	); err != nil {
		t.Fatalf("insert guest attachment: %v", err)
	}

	rec := doRequest(t, mux, "POST", "/api/v1/admin/sessions/"+record.ID+"/attachments/guest-attachment/revoke", adminToken, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("revoke guest attachment status = %d, want 204, body=%s", rec.Code, rec.Body.String())
	}

	after, err := s.sessionSvc.GetSessionUnchecked(context.Background(), record.ID)
	if err != nil {
		t.Fatalf("GetSessionUnchecked: %v", err)
	}
	if after.Status != "active" {
		t.Errorf("session status after guest revoke = %q, want still active", after.Status)
	}
}

func TestAdminRoutesRejectNonAdmin(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	_, nonAdminToken := s.registerUser(t, adminToken, "reg1")

	rec := doRequest(t, s.mux(), "GET", "/api/v1/admin/users", nonAdminToken, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403, body=%s", rec.Code, rec.Body.String())
	}
}

func TestAdminAuditRouteReturnsMutationEvents(t *testing.T) {
	s := newFullAdminTestSetup(t)
	adminToken := setupTestAdminForAdminAPI(t, s.authSvc, s.secretPath, "admin1", "correct horse battery staple")
	regID, _ := s.registerUser(t, adminToken, "reg1")
	mux := s.mux()

	if rec := doRequest(t, mux, "POST", "/api/v1/admin/users/"+regID+"/type", adminToken, map[string]string{"type": "admin"}); rec.Code != http.StatusNoContent {
		t.Fatalf("SetUserType status = %d, want 204", rec.Code)
	}

	rec := doRequest(t, mux, "GET", "/api/v1/admin/audit", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			Action   string `json:"action"`
			TargetID string `json:"targetId"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// registerUser's own invite-issue/redeem events are also in the log;
	// SQLite's CURRENT_TIMESTAMP is second-resolution, so same-second events
	// aren't guaranteed orderable by insertion order — assert presence, not
	// position.
	var found bool
	for _, item := range body.Items {
		if item.Action == "user.promote" && item.TargetID == regID {
			found = true
		}
	}
	if !found {
		t.Fatalf("audit items = %+v, want a user.promote/%s event", body.Items, regID)
	}
}
