package main

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"mako/internal/controlplane/admin"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/image"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"
	"mako/internal/controlplane/workerproc"

	_ "modernc.org/sqlite"
)

// newTestServiceForMain constructs every service newClientMux/newAdminMux
// need, sharing one real temp-file DB (migrated) and a real temp-file
// bootstrap secret, following the exact pattern established by every other
// test file in this plan — no in-memory shortcuts or test-only backdoors.
// The placement.Selector's Execer is a fake that never actually spawns
// anything; none of these tests exercise instance/session/transport
// routes, just mux wiring.
func newTestServiceForMain(t *testing.T) (*auth.Service, *instance.Service, *session.Service, *transport.Service, *admin.Service, *image.Service, string) {
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
	placementSelector := placement.NewSelector(&workerproc.FakeExecer{}, "fake-mako-worker")
	instanceSvc := instance.NewService(db, placementSelector)
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, svc, keyedMutex, 5*time.Minute)
	transportSvc := transport.NewService(db, sessionSvc, instanceSvc, keyedMutex)
	adminSvc := admin.NewService(db, svc, sessionSvc, instanceSvc)
	imageSvc := image.NewService(db, placementSelector)
	instanceSvc.SetBaseImageResolver(imageSvc)
	return svc, instanceSvc, sessionSvc, transportSvc, adminSvc, imageSvc, secretPath
}

func TestAdminMuxHealthz(t *testing.T) {
	svc, instanceSvc, _, transportSvc, adminSvc, imageSvc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newAdminMux(svc, instanceSvc, adminSvc, transportSvc, imageSvc, nil, nil))
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
	svc, instanceSvc, sessionSvc, transportSvc, _, imageSvc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newClientMux(svc, instanceSvc, sessionSvc, transportSvc, imageSvc, nil, nil))
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
	svc, instanceSvc, sessionSvc, transportSvc, _, imageSvc, _ := newTestServiceForMain(t)
	srv := httptest.NewServer(newClientMux(svc, instanceSvc, sessionSvc, transportSvc, imageSvc, nil, nil))
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
