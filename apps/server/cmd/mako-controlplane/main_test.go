package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAdminMuxHealthz(t *testing.T) {
	srv := httptest.NewServer(newAdminMux())
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
	srv := httptest.NewServer(newClientMux())
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
	srv := httptest.NewServer(newClientMux())
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
