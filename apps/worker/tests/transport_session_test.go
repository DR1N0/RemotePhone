package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"mako-worker/internal/api"
	"mako-worker/internal/runtime"
)

type transportRuntime struct {
	transportResponse runtime.TransportSession
	transportErr      error
	transportCalls    int
	lastTransport     runtime.TransportSessionRequest
}

func (r *transportRuntime) RuntimeClass() string {
	return "android-studio-emulator"
}

func (r *transportRuntime) DisplayName() string {
	return "Android Studio Emulator"
}

func (r *transportRuntime) DiscoverTemplates() ([]runtime.Template, error) {
	return nil, nil
}

func (r *transportRuntime) Start(req runtime.StartRequest) (runtime.InstanceHandle, error) {
	return runtime.InstanceHandle{}, nil
}

func (r *transportRuntime) Inspect(instanceID string) (runtime.InstanceStatus, error) {
	return runtime.InstanceStatus{}, runtime.ErrInstanceNotFound
}

func (r *transportRuntime) Stop(instanceID string) (runtime.InstanceStatus, error) {
	return runtime.InstanceStatus{}, runtime.ErrInstanceNotFound
}

func (r *transportRuntime) Reconcile() ([]runtime.InstanceStatus, error) {
	return nil, nil
}

func (r *transportRuntime) CreateTransportSession(req runtime.TransportSessionRequest) (runtime.TransportSession, error) {
	r.transportCalls++
	r.lastTransport = req
	if r.transportErr != nil {
		return runtime.TransportSession{}, r.transportErr
	}

	response := r.transportResponse
	if response.SessionID == "" {
		response.SessionID = req.SessionID
	}
	if response.AttachmentID == "" {
		response.AttachmentID = req.AttachmentID
	}
	if response.InstanceID == "" {
		response.InstanceID = req.InstanceID
	}
	if response.RuntimeClass == "" {
		response.RuntimeClass = req.RuntimeClass
	}
	if response.Mode == "" {
		response.Mode = "prototype-webrtc"
	}
	if response.Status == "" {
		response.Status = "stub"
	}
	if response.WorkerBaseURL == "" {
		response.WorkerBaseURL = req.WorkerBaseURL
	}
	return response, nil
}

func TestTransportSessionEndpointUsesRuntimeAdapterTransportHook(t *testing.T) {
	adapter := &transportRuntime{
		transportResponse: runtime.TransportSession{
			SessionToken: "adapter-token-session-1",
		},
	}
	registry := runtime.NewRegistry(adapter)
	router := api.NewRouter(api.Dependencies{
		WorkerID:        "local",
		ListenBaseURL:   "http://127.0.0.1:7780/",
		HostInventory:   api.HostInventory{HostOS: "macos", Arch: "arm64"},
		RuntimeRegistry: registry,
	})

	body := bytes.NewBufferString(`{
		"sessionId": "session-1",
		"attachmentId": "attachment-1",
		"instanceId": "instance-1",
		"runtimeClass": "android-studio-emulator",
		"runtimeHandle": {
			"workerId": "local",
			"pid": 4321,
			"consolePort": 5600,
			"adbPort": 5601,
			"grpcPort": 8554
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/transport/session", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
	}

	var response api.TransportSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if adapter.transportCalls != 1 {
		t.Fatalf("expected adapter transport hook to be called once, got %d", adapter.transportCalls)
	}
	if adapter.lastTransport.WorkerBaseURL != "http://127.0.0.1:7780" {
		t.Fatalf("expected trimmed worker base url, got %q", adapter.lastTransport.WorkerBaseURL)
	}
	if response.SessionID != "session-1" {
		t.Fatalf("expected session id session-1, got %q", response.SessionID)
	}
	if response.AttachmentID != "attachment-1" {
		t.Fatalf("expected attachment id attachment-1, got %q", response.AttachmentID)
	}
	if response.InstanceID != "instance-1" {
		t.Fatalf("expected instance id instance-1, got %q", response.InstanceID)
	}
	if response.RuntimeClass != "android-studio-emulator" {
		t.Fatalf("expected runtime class android-studio-emulator, got %q", response.RuntimeClass)
	}
	if response.Mode != "prototype-webrtc" {
		t.Fatalf("expected prototype-webrtc mode, got %q", response.Mode)
	}
	if response.Status != "stub" {
		t.Fatalf("expected stub status, got %q", response.Status)
	}
	if response.WorkerBaseURL != "http://127.0.0.1:7780" {
		t.Fatalf("expected worker base url http://127.0.0.1:7780, got %q", response.WorkerBaseURL)
	}
	if response.SessionToken != "adapter-token-session-1" {
		t.Fatalf("expected adapter-provided session token, got %q", response.SessionToken)
	}
}
