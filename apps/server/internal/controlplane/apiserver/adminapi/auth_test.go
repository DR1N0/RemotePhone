package adminapi_test

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

	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

// newTestServiceForAdminAPI constructs a *auth.Service backed by a real
// temp-file DB (migrated) and a real temp-file bootstrap secret, following
// the exact pattern established by middleware's newTestAuthService — no
// in-memory shortcuts or test-only backdoors.
func newTestServiceForAdminAPI(t *testing.T) (*auth.Service, string) {
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

// setupTestAdminForAdminAPI drives a real SetupAdmin call using the
// bootstrap secret newTestServiceForAdminAPI wrote to secretPath, and
// returns the resulting account bearer token.
func setupTestAdminForAdminAPI(t *testing.T, svc *auth.Service, secretPath, username, password string) string {
	t.Helper()
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}
	_, token, err := svc.SetupAdmin(context.Background(), auth.SetupAdminInput{
		Username:        username,
		Password:        password,
		DeviceLabel:     "setup-device",
		BootstrapSecret: string(secret),
	})
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	return token
}

func TestSetupRejectsNonLoopbackRequest(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/setup", nil)
	req.RemoteAddr = "10.0.0.5:12345" // not loopback
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a non-loopback setup request", rec.Code)
	}
}

func TestSetupRejectsForwardedLoopbackSpoof(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/setup", nil)
	req.RemoteAddr = "10.0.0.5:12345"              // the REAL peer is remote
	req.Header.Set("X-Forwarded-For", "127.0.0.1") // attempted spoof
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 — a forwarded-header loopback claim must not bypass the real peer-address check", rec.Code)
	}
}

func TestSetupRejectsNonLocalHostHeaderEvenFromLoopbackPeer(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/setup", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "evil.example.com"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a loopback peer with a non-local Host header", rec.Code)
	}
}

func TestSetupSucceedsFromLoopbackWithLocalHost(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}

	body, _ := json.Marshal(map[string]string{
		"username":        "admin",
		"password":        "correct horse battery staple",
		"deviceLabel":     "d",
		"bootstrapSecret": string(secret),
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/setup", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "localhost:8080"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" {
		t.Error("token was empty")
	}
}

func TestSetupNormalizesUsernameLikeLogin(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}

	// A mixed-case, whitespace-padded username must be accepted and stored
	// in its normalized (trimmed, lowercased) form, matching /login and
	// /register's normalization behavior.
	body, _ := json.Marshal(map[string]string{
		"username":        "  ADMIN_1  ",
		"password":        "correct horse battery staple",
		"deviceLabel":     "d",
		"bootstrapSecret": string(secret),
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/setup", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "localhost:8080"
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
	if resp.User.Username != "admin_1" {
		t.Errorf("username = %q, want normalized %q", resp.User.Username, "admin_1")
	}
	if resp.Token == "" {
		t.Error("token was empty")
	}
}

func TestSetupRejectsInvalidBootstrapSecret(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]string{
		"username":        "admin",
		"password":        "correct horse battery staple",
		"deviceLabel":     "d",
		"bootstrapSecret": "wrong-secret",
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/setup", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "localhost:8080"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for an invalid bootstrap secret", rec.Code)
	}
}

func TestSetupRejectsAlreadyClaimedServer(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	setupTestAdminForAdminAPI(t, svc, secretPath, "admin", "correct horse battery staple")

	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]string{
		"username":        "second",
		"password":        "correct horse battery staple",
		"deviceLabel":     "d",
		"bootstrapSecret": "whatever",
	})
	req := httptest.NewRequest("POST", "/api/v1/auth/setup", bytes.NewReader(body))
	req.RemoteAddr = "127.0.0.1:54321"
	req.Host = "localhost:8080"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 once the server is already claimed", rec.Code)
	}
}

func TestStatusReportsClaimedState(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var resp map[string]bool
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp["claimed"] {
		t.Error("claimed = true before any admin exists")
	}

	setupTestAdminForAdminAPI(t, svc, secretPath, "admin", "correct horse battery staple")

	req2 := httptest.NewRequest("GET", "/api/v1/auth/status", nil)
	rec2 := httptest.NewRecorder()
	mux.ServeHTTP(rec2, req2)
	var resp2 map[string]bool
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp2["claimed"] {
		t.Error("claimed = false after SetupAdmin succeeded")
	}
}

func TestRegistrationInviteRequiresAuth(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/registration-invites", bytes.NewReader([]byte(`{}`)))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 without a bearer token", rec.Code)
	}
}

func TestRegistrationInviteSucceedsForAdmin(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	token := setupTestAdminForAdminAPI(t, svc, secretPath, "admin", "correct horse battery staple")

	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	req := httptest.NewRequest("POST", "/api/v1/auth/registration-invites", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expiresAt"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" || resp.ExpiresAt == "" {
		t.Errorf("response missing fields: %+v", resp)
	}
	if len(resp.Token) != 4 {
		t.Errorf("token = %q, want a 4-digit code", resp.Token)
	}
}

// TestRegistrationInviteIgnoresRequestBody covers the route's current
// shape: ttlMs used to be a caller-configurable field here, but
// auth.CreateRegistrationInvite now issues a fixed-TTL 4-digit code (see
// auth.registrationInviteTTL) and the route no longer parses a request
// body at all — any body, including a stale ttlMs client still sends, is
// simply ignored rather than rejected.
func TestRegistrationInviteIgnoresRequestBody(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	token := setupTestAdminForAdminAPI(t, svc, secretPath, "admin", "correct horse battery staple")

	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]int64{"ttlMs": 1000})
	req := httptest.NewRequest("POST", "/api/v1/auth/registration-invites", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201 (request body is no longer parsed)", rec.Code)
	}
}

func TestLoginAndMeRoutesAreRegisteredOnAdminAPI(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	setupTestAdminForAdminAPI(t, svc, secretPath, "realuser", "correct horse battery staple")

	mux := http.NewServeMux()
	adminapi.RegisterAuthRoutes(mux, svc)

	body, _ := json.Marshal(map[string]string{"username": "realuser", "password": "correct horse battery staple", "deviceLabel": "d"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — commonauth routes must be mounted by RegisterAuthRoutes, body=%s", rec.Code, rec.Body.String())
	}
}
