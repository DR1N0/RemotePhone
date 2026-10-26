package adminapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/auth"
)

func ownerRequest(t *testing.T, mux *http.ServeMux, method, path, body, origin string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "127.0.0.1:3001"
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestOwnerAccessCodeNeedsNoToken(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterOwnerRoutes(mux, svc)

	rec := ownerRequest(t, mux, http.MethodPost, "/api/v1/owner/access-codes", `{}`, "")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Code == "" {
		t.Fatalf("invalid code response: %s", rec.Body.String())
	}
}

func TestOwnerRejectsBrowserOrigin(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterOwnerRoutes(mux, svc)

	rec := ownerRequest(t, mux, http.MethodPost, "/api/v1/owner/access-codes", `{}`, "http://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestOwnerResetPasswordAndLogin(t *testing.T) {
	svc, secretPath := newTestServiceForAdminAPI(t)
	setupTestAdminForAdminAPI(t, svc, secretPath, "owner_admin", "initial-password")
	mux := http.NewServeMux()
	adminapi.RegisterOwnerRoutes(mux, svc)

	rec := ownerRequest(t, mux, http.MethodPost, "/api/v1/owner/users/reset-password",
		`{"username":"owner_admin","password":"recovered-password"}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("reset status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if _, _, err := svc.Login(context.Background(), auth.LoginInput{
		Username: "owner_admin", Password: "recovered-password", DeviceLabel: "test",
	}); err != nil {
		t.Fatalf("login with reset password: %v", err)
	}
}
