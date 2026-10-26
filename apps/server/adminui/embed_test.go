package adminui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerServesIndexAndSPAFallback(t *testing.T) {
	handler := Handler()
	for _, path := range []string{"/admin/", "/admin/users"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "<div id=\"root\">") {
			t.Fatalf("%s did not return the SPA index", path)
		}
	}
}

func TestHandlerRejectsNonAdminPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/server", nil)
	rec := httptest.NewRecorder()
	Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}
