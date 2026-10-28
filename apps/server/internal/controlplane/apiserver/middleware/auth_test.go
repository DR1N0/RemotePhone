package middleware

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

// newTestAuthService constructs a *auth.Service backed by a real temp-file
// bootstrap secret (not an empty path) specifically so mustIssueTestToken
// can drive a real SetupAdmin call — auth.Service exposes no test-only
// backdoor for creating a user, by design.
func newTestAuthService(t *testing.T) (*auth.Service, string) {
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

func protectedHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := GetAuthenticatedUser(r.Context())
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(user.UserID))
	})
}

func TestRequireAuthRejectsMissingToken(t *testing.T) {
	svc, _ := newTestAuthService(t)
	handler := RequireAuth(svc)(protectedHandler())

	req := httptest.NewRequest("GET", "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a missing token", rec.Code)
	}
}

func TestRequireAuthRejectsWrongScheme(t *testing.T) {
	svc, _ := newTestAuthService(t)
	handler := RequireAuth(svc)(protectedHandler())

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Basic dXNlcjpwYXNz")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for a non-Bearer scheme", rec.Code)
	}
}

// TestRequireAuthPanicsOnInfrastructureError confirms the middleware fix:
// a non-nil error from svc.Authenticate (an infrastructure failure, per its
// doc comment) must panic rather than being collapsed into the same 401 as
// an ordinary invalid/revoked credential. It forces a real, non-sql.ErrNoRows
// error out of Authenticate by closing the backing *sql.DB out from under a
// token that would otherwise be valid, then relies on recover() since this
// test calls handler.ServeHTTP directly rather than going through a real
// net/http.Server (whose per-request panic recovery is what the production
// code path relies on).
func TestRequireAuthPanicsOnInfrastructureError(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if err := persistence.ApplyMigrations(db); err != nil {
		t.Fatalf("ApplyMigrations: %v", err)
	}
	secretPath := filepath.Join(t.TempDir(), "bootstrap-secret")
	svc, err := auth.NewService(db, secretPath)
	if err != nil {
		t.Fatalf("auth.NewService: %v", err)
	}
	token := mustIssueTestToken(t, svc, secretPath)

	// Close the DB out from under an otherwise-valid token so Authenticate's
	// GetCredentialByTokenHash call returns a real "database is closed"
	// error rather than sql.ErrNoRows.
	if err := db.Close(); err != nil {
		t.Fatalf("db.Close: %v", err)
	}

	defer func() {
		if recover() == nil {
			t.Error("RequireAuth did not panic on a genuine Authenticate infrastructure error")
		}
	}()

	handler := RequireAuth(svc)(protectedHandler())
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
}

func TestRequireAuthAcceptsValidToken(t *testing.T) {
	svc, secretPath := newTestAuthService(t)

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+mustIssueTestToken(t, svc, secretPath))
	rec := httptest.NewRecorder()
	handler := RequireAuth(svc)(protectedHandler())
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 for a valid token", rec.Code)
	}
}

func TestRequireScopeRejectsMismatch(t *testing.T) {
	svc, secretPath := newTestAuthService(t)
	token := mustIssueTestToken(t, svc, secretPath)

	handler := RequireAuth(svc)(RequireScope("attachment")(protectedHandler()))
	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for an account-scoped token on an attachment-scoped route", rec.Code)
	}
}

func TestIsLoopbackAddress(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":        true,
		"::1":              true,
		"::ffff:127.0.0.1": true,
		"127.0.0.2":        false,
		"10.0.0.5":         false,
		"":                 false,
	}
	for addr, want := range cases {
		if got := IsLoopbackAddress(addr); got != want {
			t.Errorf("IsLoopbackAddress(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestWriteJSONAndWriteJSONError(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusCreated, map[string]string{"id": "abc"})
	if rec.Code != http.StatusCreated {
		t.Errorf("WriteJSON status = %d, want 201", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if body := rec.Body.String(); body != `{"id":"abc"}`+"\n" {
		t.Errorf("WriteJSON body = %q, want {\"id\":\"abc\"}\\n", body)
	}

	rec2 := httptest.NewRecorder()
	WriteJSONError(rec2, http.StatusBadRequest, "bad input")
	if rec2.Code != http.StatusBadRequest {
		t.Errorf("WriteJSONError status = %d, want 400", rec2.Code)
	}
	if body := rec2.Body.String(); body != `{"error":"bad input"}`+"\n" {
		t.Errorf("WriteJSONError body = %q, want {\"error\":\"bad input\"}\\n", body)
	}
}

func TestGetAuthenticatedUserPanicsWithoutMiddleware(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("GetAuthenticatedUser did not panic when called outside RequireAuth's chain")
		}
	}()
	req := httptest.NewRequest("GET", "/", nil)
	GetAuthenticatedUser(req.Context())
}

// mustIssueTestToken drives a real SetupAdmin call using the bootstrap
// secret newTestAuthService wrote to secretPath, and returns the resulting
// account bearer token — using only *auth.Service's exported surface, since
// this test lives in a different package than auth's own tests and must
// not reach into its unexported fields.
func mustIssueTestToken(t *testing.T, svc *auth.Service, secretPath string) string {
	t.Helper()
	secret, err := os.ReadFile(secretPath)
	if err != nil {
		t.Fatalf("read bootstrap secret: %v", err)
	}
	_, token, err := svc.SetupAdmin(context.Background(), auth.SetupAdminInput{
		Username: "admin", Password: "correct horse battery staple",
		DeviceLabel: "test-device", BootstrapSecret: string(secret),
	})
	if err != nil {
		t.Fatalf("SetupAdmin: %v", err)
	}
	return token
}
