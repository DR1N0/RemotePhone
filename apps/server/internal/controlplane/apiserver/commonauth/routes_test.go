package commonauth_test

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

	"remotephone/internal/controlplane/apiserver/commonauth"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

// newTestServiceForRoutes constructs a *auth.Service backed by a real
// temp-file DB (migrated) and a real temp-file bootstrap secret, following
// the exact pattern established by middleware's newTestAuthService — no
// in-memory shortcuts or test-only backdoors.
func newTestServiceForRoutes(t *testing.T) (*auth.Service, string) {
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

// setupTestAdmin drives a real SetupAdmin call using the bootstrap secret
// newTestServiceForRoutes wrote to secretPath, creating the first admin user
// with the given username/password so later tests can log in as them.
func setupTestAdmin(t *testing.T, svc *auth.Service, secretPath, username, password string) {
	t.Helper()
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}
	_, _, err = svc.SetupAdmin(context.Background(), auth.SetupAdminInput{
		Username:        username,
		Password:        password,
		DeviceLabel:     "setup-device",
		BootstrapSecret: string(secret),
	})
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
}

func TestLoginAntiEnumerationViaHTTP(t *testing.T) {
	svc, secretPath := newTestServiceForRoutes(t)
	setupTestAdmin(t, svc, secretPath, "realuser", "correct horse battery staple")

	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	// Both passwords must independently satisfy the 12-1024 char shape
	// validation (so a shape-rejected 400 can never masquerade as the 401
	// anti-enumeration path below) — "whatever" alone is only 8 chars, so
	// it is lengthened here to actually reach svc.Login.
	unknownRec := doLogin(t, mux, "nosuchuser", "whatever-password")
	wrongRec := doLogin(t, mux, "realuser", "wrong-password")

	if unknownRec.Code != http.StatusUnauthorized || wrongRec.Code != http.StatusUnauthorized {
		t.Fatalf("status codes = %d, %d, want both 401", unknownRec.Code, wrongRec.Code)
	}
	if unknownRec.Body.String() != wrongRec.Body.String() {
		t.Errorf("response bodies differ: %q vs %q — anti-enumeration broken", unknownRec.Body.String(), wrongRec.Body.String())
	}
}

func TestLoginSuccessReturnsUserAndToken(t *testing.T) {
	svc, secretPath := newTestServiceForRoutes(t)
	setupTestAdmin(t, svc, secretPath, "realuser", "correct horse battery staple")

	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	rec := doLogin(t, mux, "realuser", "correct horse battery staple")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User  map[string]any `json:"user"`
		Token string         `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Token == "" {
		t.Error("token was empty")
	}
	if resp.User["username"] != "realuser" {
		t.Errorf("user.username = %v, want realuser", resp.User["username"])
	}
}

func TestLoginRejectsInvalidRequestShape(t *testing.T) {
	svc, _ := newTestServiceForRoutes(t)
	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	body, _ := json.Marshal(map[string]string{"username": "ab", "password": "short", "deviceLabel": "d"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for an invalid login request shape", rec.Code)
	}
}

func TestLoginThrottleBlocksAfterRepeatedFailures(t *testing.T) {
	svc, secretPath := newTestServiceForRoutes(t)
	setupTestAdmin(t, svc, secretPath, "realuser", "correct horse battery staple")

	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	var lastRec *httptest.ResponseRecorder
	for i := 0; i < 5; i++ {
		lastRec = doLogin(t, mux, "realuser", "wrong-password")
		if lastRec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d, want 401", i, lastRec.Code)
		}
	}

	throttledRec := doLogin(t, mux, "realuser", "wrong-password")
	if throttledRec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 after 5 failed attempts", throttledRec.Code)
	}

	// Even the CORRECT password must now be throttled — the limiter blocks
	// the key outright, it does not special-case a subsequently-correct
	// attempt.
	correctButThrottledRec := doLogin(t, mux, "realuser", "correct horse battery staple")
	if correctButThrottledRec.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 even for a correct password once throttled", correctButThrottledRec.Code)
	}
}

func TestMeReturnsFreshUserState(t *testing.T) {
	svc, secretPath := newTestServiceForRoutes(t)
	setupTestAdmin(t, svc, secretPath, "realuser", "correct horse battery staple")

	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	loginRec := doLogin(t, mux, "realuser", "correct horse battery staple")
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	req.Header.Set("Authorization", "Bearer "+loginResp.Token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var resp struct {
		User map[string]any `json:"user"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.User["username"] != "realuser" {
		t.Errorf("user.username = %v, want realuser", resp.User["username"])
	}
}

func TestMeRejectsMissingToken(t *testing.T) {
	svc, _ := newTestServiceForRoutes(t)
	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	req := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a missing token", rec.Code)
	}
}

func TestLogoutRevokesCredential(t *testing.T) {
	svc, secretPath := newTestServiceForRoutes(t)
	setupTestAdmin(t, svc, secretPath, "realuser", "correct horse battery staple")

	mux := http.NewServeMux()
	commonauth.RegisterRoutes(mux, svc, false)

	loginRec := doLogin(t, mux, "realuser", "correct horse battery staple")
	var loginResp struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	logoutReq := httptest.NewRequest("POST", "/api/v1/auth/logout", nil)
	logoutReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	logoutRec := httptest.NewRecorder()
	mux.ServeHTTP(logoutRec, logoutReq)

	if logoutRec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204, body=%s", logoutRec.Code, logoutRec.Body.String())
	}
	if logoutRec.Body.Len() != 0 {
		t.Errorf("logout body = %q, want empty", logoutRec.Body.String())
	}

	meReq := httptest.NewRequest("GET", "/api/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+loginResp.Token)
	meRec := httptest.NewRecorder()
	mux.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a revoked credential used after logout", meRec.Code)
	}
}

func doLogin(t *testing.T, mux *http.ServeMux, username, password string) *httptest.ResponseRecorder {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": username, "password": password, "deviceLabel": "d"})
	req := httptest.NewRequest("POST", "/api/v1/auth/login", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}
