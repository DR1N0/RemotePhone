package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"

	_ "modernc.org/sqlite"
)

// newTestServiceForMain constructs a *auth.Service and an *instance.Service
// sharing one real temp-file DB (migrated) and a real temp-file bootstrap
// secret, following the exact pattern established by every other test file
// in this plan — no in-memory shortcuts or test-only backdoors. The
// instance.Service's worker client points nowhere real; none of these tests
// exercise instance routes.
func newTestServiceForMain(t *testing.T) (*auth.Service, *instance.Service, string) {
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
	instanceSvc := instance.NewService(db, instance.NewHTTPWorkerClient("http://127.0.0.1:0"), placement.NewSelector())
	return svc, instanceSvc, secretPath
}

func TestAdminMuxHealthz(t *testing.T) {
	svc, instanceSvc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newAdminMux(svc, instanceSvc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestClientMuxHealthz(t *testing.T) {
	svc, instanceSvc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newClientMux(svc, instanceSvc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestClientMuxHasNoAdminRoute(t *testing.T) {
	svc, instanceSvc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newClientMux(svc, instanceSvc))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/admin/anything")
	if err != nil {
		t.Fatalf("GET /admin/anything: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (admin routes must never exist on the client mux)", resp.StatusCode)
	}
}
