package tests

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"mako-worker/internal/api"
	"mako-worker/internal/transport"
)

func TestOfferEndpointReturns200WithAnswer(t *testing.T) {
	store := transport.NewStore()
	store.Put("instance-1:att-1", transport.NoopAdapter{})

	router := api.NewRouter(api.Dependencies{
		WorkerID:       "local",
		ListenBaseURL:  "http://127.0.0.1:7780/",
		HostInventory:  api.HostInventory{HostOS: "macos", Arch: "arm64"},
		TransportStore: store,
	})

	body := bytes.NewBufferString(`{
        "instanceId": "instance-1",
        "attachmentId": "att-1",
        "role": "owner",
        "sdpOffer": "v=0\r\n"
    }`)
	req := httptest.NewRequest(http.MethodPost, "/v1/transport/offer", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestOfferEndpointReturns404WhenAdapterNotFound(t *testing.T) {
	store := transport.NewStore()
	router := api.NewRouter(api.Dependencies{
		WorkerID:       "local",
		ListenBaseURL:  "http://127.0.0.1:7780/",
		HostInventory:  api.HostInventory{HostOS: "macos", Arch: "arm64"},
		TransportStore: store,
	})

	body := bytes.NewBufferString(`{"instanceId":"x","attachmentId":"y","role":"owner","sdpOffer":""}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/transport/offer", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}
