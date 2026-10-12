package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

// newTestServiceForMain constructs a *auth.Service backed by a real
// temp-file DB (migrated) and a real temp-file bootstrap secret, following
// the exact pattern established by every other test file in this plan — no
// in-memory shortcuts or test-only backdoors.
func newTestServiceForMain(t *testing.T) (*auth.Service, string) {
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

func TestAdminMuxHealthz(t *testing.T) {
	svc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newAdminMux(svc))
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
	svc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newClientMux(svc))
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
	svc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newClientMux(svc))
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
