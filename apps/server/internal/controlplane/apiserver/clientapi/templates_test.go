package clientapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"remotephone/internal/controlplane/apiserver/clientapi"
	"remotephone/internal/controlplane/instance"
)

func TestTemplateRoutesListDiscoveredTemplatesForAnAccount(t *testing.T) {
	authSvc, instanceSvc, secretPath := newTestInstanceSetup(t)
	token := mustCreateAccount(t, authSvc, secretPath, "owner1")

	mux := http.NewServeMux()
	clientapi.RegisterTemplateRoutes(mux, instanceSvc, authSvc)

	req := httptest.NewRequest("GET", "/api/v1/templates", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("templates status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Templates []instance.TemplateInfo `json:"templates"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode templates response: %v", err)
	}
	if len(body.Templates) != 1 || body.Templates[0].TemplateID != "t-1" {
		t.Fatalf("templates = %+v, want exactly t-1", body.Templates)
	}
	if body.Templates[0].DisplayName != "T1" {
		t.Errorf("displayName = %q, want T1", body.Templates[0].DisplayName)
	}
}

func TestTemplateRoutesRequireAuthentication(t *testing.T) {
	authSvc, instanceSvc, _ := newTestInstanceSetup(t)

	mux := http.NewServeMux()
	clientapi.RegisterTemplateRoutes(mux, instanceSvc, authSvc)

	req := httptest.NewRequest("GET", "/api/v1/templates", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated templates status = %d, want 401", rec.Code)
	}
}
