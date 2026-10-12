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
	"mako/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

// newTestServiceForClientAPI constructs a *auth.Service backed by a real
// temp-file DB (migrated) and a real temp-file bootstrap secret, following
// the exact pattern established by adminapi's newTestServiceForAdminAPI — no
// in-memory shortcuts or test-only backdoors.
func newTestServiceForClientAPI(t *testing.T) (*auth.Service, string) {
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
	svc, err := auth.NewService(db, secretPath)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	return svc, secretPath
}

// mustSetupAdminForClientAPI drives a real SetupAdmin call using the
// bootstrap secret newTestServiceForClientAPI wrote to secretPath, and
// returns the resulting account bearer token.
func mustSetupAdminForClientAPI(t *testing.T, svc *auth.Service, secretPath string) string {
	t.Helper()
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}
	_, token, err := svc.SetupAdmin(context.Background(), auth.SetupAdminInput{
		Username:        "admin",
		Password:        "correct horse battery staple",
		DeviceLabel:     "setup-device",
		BootstrapSecret: string(secret),
	})
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	return token
}

func TestRetiredPairingInviteRoutesReturn410(t *testing.T) {
	svc, _ := newTestServiceForClientAPI(t)
	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	for _, path := range []string{"/api/v1/auth/pairing-invites", "/api/v1/auth/pairing-invites/redeem"} {
		req := httptest.NewRequest("POST", path, nil)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusGone {
			t.Errorf("%s status = %d, want 410", path, rec.Code)
		}
	}
}

func TestRevokeCredentialCollapsesNotFoundAndNotOwnedTo404(t *testing.T) {
	svc, secretPath := newTestServiceForClientAPI(t)
	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)
	token := mustSetupAdminForClientAPI(t, svc, secretPath)

	// A nonexistent credential id.
	req := httptest.NewRequest("POST", "/api/v1/auth/credentials/does-not-exist/revoke", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Errorf("revoke of a nonexistent credential id: status = %d, want 404", rec.Code)
	}
}

func TestRevokeCredentialRequiresAccountScope(t *testing.T) {
	svc, _ := newTestServiceForClientAPI(t)
	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/credentials/does-not-exist/revoke", nil)
	// No Authorization header at all.
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("revoke with no credential at all: status = %d, want 401 (not 404 — auth runs first)", rec.Code)
	}
}

func TestRegisterNormalizesUsernameLikeLogin(t *testing.T) {
	svc, secretPath := newTestServiceForClientAPI(t)
	adminToken := mustSetupAdminForClientAPI(t, svc, secretPath)

	inviteToken, _, err := svc.CreateRegistrationInvite(context.Background(), mustUserIDFromToken(t, svc, adminToken), nil, "req-1", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}

	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]string{
		"inviteToken": inviteToken,
		"username":    "  NewUser_1  ",
		"password":    "correct horse battery staple",
		"deviceLabel": "d",
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.User.Username != "newuser_1" {
		t.Errorf("username = %q, want normalized %q", resp.User.Username, "newuser_1")
	}
	if resp.Token == "" {
		t.Error("token was empty")
	}
}

func TestRegisterRejectsInvalidRequestBody(t *testing.T) {
	svc, _ := newTestServiceForClientAPI(t)
	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an empty registration request", rec.Code)
	}
}

func TestRegisterRejectsInvalidInviteWithGenericBadRequest(t *testing.T) {
	svc, _ := newTestServiceForClientAPI(t)
	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]string{
		"inviteToken": "not-a-real-invite",
		"username":    "newuser",
		"password":    "correct horse battery staple",
		"deviceLabel": "d",
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid registration invite", rec.Code)
	}
}

func TestRegisterRejectsConflictingUsername(t *testing.T) {
	svc, secretPath := newTestServiceForClientAPI(t)
	adminToken := mustSetupAdminForClientAPI(t, svc, secretPath)
	adminUserID := mustUserIDFromToken(t, svc, adminToken)

	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	inviteToken, _, err := svc.CreateRegistrationInvite(context.Background(), adminUserID, nil, "req-2", nil)
	if err != nil {
		t.Fatalf("CreateRegistrationInvite: %v", err)
	}
	body, _ := json.Marshal(map[string]string{
		"inviteToken": inviteToken,
		"username":    "admin",
		"password":    "correct horse battery staple",
		"deviceLabel": "d",
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/register", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a username that is already registered", rec.Code)
	}
}

func TestCommonAuthRoutesAreRegisteredOnClientAPI(t *testing.T) {
	svc, secretPath := newTestServiceForClientAPI(t)
	mustSetupAdminForClientAPI(t, svc, secretPath)

	mux := http.NewServeMux()
	clientapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "correct horse battery staple", "deviceLabel": "d"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — commonauth routes must be mounted by RegisterAuthRoutes, body=%s", rec.Code, rec.Body.String())
	}
}

// mustUserIDFromToken authenticates token against svc and returns the
// resulting user id, so tests can issue registration invites as that user
// without any backdoor into the service's internals.
func mustUserIDFromToken(t *testing.T, svc *auth.Service, token string) string {
	t.Helper()
	user, err := svc.Authenticate(context.Background(), token)
	if err != nil || user == nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return user.UserID
}
