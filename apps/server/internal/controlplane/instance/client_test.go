package instance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	workerapi "remotephone/internal/worker/api"
	"remotephone/internal/worker/runtime"
)

func TestHTTPWorkerClientListTemplates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/templates" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(workerapi.TemplateListResponse{
			Templates: []runtime.Template{{TemplateID: "t-1", RuntimeClass: "android-studio-emulator", DisplayName: "T1"}},
		})
	}))
	defer server.Close()

	client := NewHTTPWorkerClient(server.URL)
	templates, err := client.ListTemplates(context.Background())
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(templates) != 1 || templates[0].TemplateID != "t-1" {
		t.Errorf("ListTemplates() = %+v, want one template t-1", templates)
	}
}

func TestHTTPWorkerClientStartInstance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/instances/start" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		var req workerapi.StartInstanceRequest
		json.NewDecoder(r.Body).Decode(&req)
		if req.InstanceID != "i-1" {
			t.Errorf("request body instanceId = %q, want i-1", req.InstanceID)
		}
		json.NewEncoder(w).Encode(workerapi.StartInstanceResponse{
			State:  "starting",
			Handle: runtime.InstanceHandle{InstanceID: "i-1", PID: 123},
		})
	}))
	defer server.Close()

	client := NewHTTPWorkerClient(server.URL)
	resp, err := client.StartInstance(context.Background(), workerapi.StartInstanceRequest{InstanceID: "i-1"})
	if err != nil {
		t.Fatalf("StartInstance: %v", err)
	}
	if resp.State != "starting" || resp.Handle.PID != 123 {
		t.Errorf("StartInstance() = %+v, want state=starting pid=123", resp)
	}
}

func TestHTTPWorkerClientNonOKStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	client := NewHTTPWorkerClient(server.URL)
	_, err := client.InspectInstance(context.Background(), "missing")
	if err == nil {
		t.Fatal("InspectInstance() = nil error, want ErrWorkerRequestFailed")
	}
	var reqErr *ErrWorkerRequestFailed
	if !errors.As(err, &reqErr) {
		t.Fatalf("InspectInstance() error = %v, want *ErrWorkerRequestFailed", err)
	}
	if reqErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", reqErr.StatusCode)
	}
}
