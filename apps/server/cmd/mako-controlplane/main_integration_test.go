package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminAndClientMuxesServeRealAuthRoutes(t *testing.T) {
	svc, instanceSvc, sessionSvc, transportSvc, _ := newTestServiceForMain(t) // same real-DB-plus-bootstrap-secret pattern as every other test file

	adminSrv := httptest.NewServer(newAdminMux(svc, instanceSvc))
	defer adminSrv.Close()
	clientSrv := httptest.NewServer(newClientMux(svc, instanceSvc, sessionSvc, transportSvc))
	defer clientSrv.Close()

	// /auth/status exists on the admin surface...
	resp, err := http.Get(adminSrv.URL + "/api/v1/auth/status")
	if err != nil {
		t.Fatalf("GET admin /auth/status: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("admin /auth/status status = %d, want 200", resp.StatusCode)
	}

	// ...but does NOT exist on the client surface.
	resp2, err := http.Get(clientSrv.URL + "/api/v1/auth/status")
	if err != nil {
		t.Fatalf("GET client /auth/status: %v", err)
	}
	if resp2.StatusCode != http.StatusNotFound {
		t.Errorf("client /auth/status status = %d, want 404 (admin-only route must not exist on the client surface)", resp2.StatusCode)
	}

	// /auth/register exists on the client surface but not the admin surface.
	resp3, err := http.Post(clientSrv.URL+"/api/v1/auth/register", "application/json", nil)
	if err != nil {
		t.Fatalf("POST client /auth/register: %v", err)
	}
	if resp3.StatusCode == http.StatusNotFound {
		t.Error("client /auth/register returned 404, want it registered on this surface (whatever its body-validation status is)")
	}

	resp4, err := http.Post(adminSrv.URL+"/api/v1/auth/register", "application/json", nil)
	if err != nil {
		t.Fatalf("POST admin /auth/register: %v", err)
	}
	if resp4.StatusCode != http.StatusNotFound {
		t.Errorf("admin /auth/register status = %d, want 404 (client-only route must not exist on the admin surface)", resp4.StatusCode)
	}
}
