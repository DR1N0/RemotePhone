package tests

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remotephone/internal/worker/api"
	"remotephone/internal/worker/runtime"
	"remotephone/internal/worker/transport"
)

type recordingTransportAdapter struct {
	role         string
	setRoleCalls int
	stopCalls    int
}

func (a *recordingTransportAdapter) HandleOffer(_, _, _ string) (string, error) {
	return "sdp-answer", nil
}

func (a *recordingTransportAdapter) HandleICECandidate(string) error { return nil }

func (a *recordingTransportAdapter) HandleInput(transport.InputEvent) error { return nil }

func (a *recordingTransportAdapter) SetRole(role string) {
	a.role = role
	a.setRoleCalls++
}

func (a *recordingTransportAdapter) Role() string { return a.role }

func (a *recordingTransportAdapter) Stop() error {
	a.stopCalls++
	return nil
}

func TestTransportDestroyStopsAndRemovesAdapter(t *testing.T) {
	store := transport.NewStore()
	adapter := &recordingTransportAdapter{}
	key := "instance-1:attachment-1"
	store.Put(key, adapter)

	router := api.NewRouter(api.Dependencies{TransportStore: store})
	req := httptest.NewRequest(http.MethodDelete, "/v1/transport/session?instanceId=instance-1&attachmentId=attachment-1", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d with body %s", rec.Code, rec.Body.String())
	}
	if adapter.stopCalls != 1 {
		t.Fatalf("expected Stop once, got %d", adapter.stopCalls)
	}
	if _, ok := store.Get(key); ok {
		t.Fatal("expected adapter to be removed")
	}
}

func TestStoppingInstanceStopsAllInstanceTransports(t *testing.T) {
	store := transport.NewStore()
	instanceOneFirst := &recordingTransportAdapter{}
	instanceOneSecond := &recordingTransportAdapter{}
	instanceTwo := &recordingTransportAdapter{}
	store.Put("instance-1:attachment-1", instanceOneFirst)
	store.Put("instance-1:attachment-2", instanceOneSecond)
	store.Put("instance-2:attachment-1", instanceTwo)

	runtimeAdapter := &routerRuntime{
		stopResponse: runtime.InstanceStatus{State: "stopped"},
	}
	router := api.NewRouter(api.Dependencies{
		RuntimeRegistry: runtime.NewRegistry(runtimeAdapter),
		TransportStore:  store,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-1/stop", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
	}
	if instanceOneFirst.stopCalls != 1 || instanceOneSecond.stopCalls != 1 {
		t.Fatalf("expected instance-1 adapters to stop once, got %d and %d", instanceOneFirst.stopCalls, instanceOneSecond.stopCalls)
	}
	if instanceTwo.stopCalls != 0 {
		t.Fatalf("expected instance-2 adapter not to stop, got %d", instanceTwo.stopCalls)
	}
	if _, ok := store.Get("instance-1:attachment-1"); ok {
		t.Fatal("expected first instance-1 adapter to be removed")
	}
	if _, ok := store.Get("instance-1:attachment-2"); ok {
		t.Fatal("expected second instance-1 adapter to be removed")
	}
	if _, ok := store.Get("instance-2:attachment-1"); !ok {
		t.Fatal("expected instance-2 adapter to remain")
	}
}

func TestTransportRoleUpdateRejectsInvalidRole(t *testing.T) {
	store := transport.NewStore()
	adapter := &recordingTransportAdapter{role: "owner"}
	store.Put("instance-1:attachment-1", adapter)

	router := api.NewRouter(api.Dependencies{TransportStore: store})
	req := httptest.NewRequest(http.MethodPatch, "/v1/transport/session/role", strings.NewReader(`{"instanceId":"instance-1","attachmentId":"attachment-1","role":"administrator"}`))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d with body %s", rec.Code, rec.Body.String())
	}
	if adapter.setRoleCalls != 0 || adapter.Role() != "owner" {
		t.Fatalf("expected owner role to remain unchanged after %d updates, got %q", adapter.setRoleCalls, adapter.Role())
	}
}

func TestOfferCannotChangeStoredObserverRole(t *testing.T) {
	for _, offeredRole := range []string{"owner", "participant"} {
		t.Run(offeredRole, func(t *testing.T) {
			store := transport.NewStore()
			adapter := &recordingTransportAdapter{role: "observer"}
			store.Put("instance-1:attachment-1", adapter)

			router := api.NewRouter(api.Dependencies{TransportStore: store})
			req := httptest.NewRequest(http.MethodPost, "/v1/transport/offer", bytes.NewBufferString(`{"instanceId":"instance-1","attachmentId":"attachment-1","role":"`+offeredRole+`","sdpOffer":"v=0\\r\\n"}`))
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
			}
			if adapter.setRoleCalls != 0 || adapter.Role() != "observer" {
				t.Fatalf("expected observer role after %d updates, got %q", adapter.setRoleCalls, adapter.Role())
			}
		})
	}
}

func TestTransportRoleUpdateChangesExistingAdapter(t *testing.T) {
	store := transport.NewStore()
	adapter := &recordingTransportAdapter{role: "owner"}
	store.Put("instance-1:attachment-1", adapter)

	router := api.NewRouter(api.Dependencies{TransportStore: store})
	req := httptest.NewRequest(http.MethodPatch, "/v1/transport/session/role", strings.NewReader(`{"instanceId":"instance-1","attachmentId":"attachment-1","role":"observer"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d with body %s", rec.Code, rec.Body.String())
	}
	if adapter.setRoleCalls != 1 || adapter.Role() != "observer" {
		t.Fatalf("expected observer role to be set once, got role %q after %d calls", adapter.Role(), adapter.setRoleCalls)
	}
}
