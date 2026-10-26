package adminapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"mako/internal/controlplane/apiserver/adminapi"
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
	adminapi.RegisterOwnerRoutes(mux, svc, nil, nil)

	rec := ownerRequest(t, mux, http.MethodPost, "/api/v1/admin/access-codes", `{}`, "")
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
	adminapi.RegisterOwnerRoutes(mux, svc, nil, nil)

	rec := ownerRequest(t, mux, http.MethodPost, "/api/v1/admin/access-codes", `{}`, "http://evil.example")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestOwnerAllowsSameOrigin(t *testing.T) {
	svc, _ := newTestServiceForAdminAPI(t)
	mux := http.NewServeMux()
	adminapi.RegisterOwnerRoutes(mux, svc, nil, nil)

	rec := ownerRequest(t, mux, http.MethodPost, "/api/v1/admin/access-codes", `{}`, "http://127.0.0.1:3001")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
}
