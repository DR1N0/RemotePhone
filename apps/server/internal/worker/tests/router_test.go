package tests

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"remotephone/internal/worker/api"
	"remotephone/internal/worker/runtime"
)

type routerRuntime struct {
	runtimeClass   string
	displayName    string
	templates      []runtime.Template
	startResponse  runtime.InstanceHandle
	inspect        runtime.InstanceStatus
	inspectErr     error
	inspectCalls   int
	lastInspectID  string
	stopResponse   runtime.InstanceStatus
	stopErr        error
	stopCalls      int
	lastStopID     string
	reconcileCalls int
}

func (r *routerRuntime) RuntimeClass() string {
	if r.runtimeClass == "" {
		return "android-studio-emulator"
	}
	return r.runtimeClass
}

func (r *routerRuntime) DisplayName() string {
	if r.displayName == "" {
		return "Android Studio Emulator"
	}
	return r.displayName
}

func (r *routerRuntime) DiscoverTemplates() ([]runtime.Template, error) {
	return r.templates, nil
}

func (r *routerRuntime) Start(req runtime.StartRequest) (runtime.InstanceHandle, error) {
	r.startResponse.InstanceID = req.InstanceID
	r.startResponse.TemplateID = req.TemplateID
	return r.startResponse, nil
}

func (r *routerRuntime) Inspect(instanceID string) (runtime.InstanceStatus, error) {
	r.inspectCalls++
	r.lastInspectID = instanceID
	if r.inspectErr != nil {
		return runtime.InstanceStatus{}, r.inspectErr
	}
	if r.inspect.Handle.InstanceID == "" {
		r.inspect.Handle.InstanceID = instanceID
	}
	return r.inspect, nil
}

func (r *routerRuntime) Stop(instanceID string) (runtime.InstanceStatus, error) {
	r.stopCalls++
	r.lastStopID = instanceID
	if r.stopErr != nil {
		return runtime.InstanceStatus{}, r.stopErr
	}
	if r.stopResponse.Handle.InstanceID == "" {
		r.stopResponse.Handle.InstanceID = instanceID
	}
	return r.stopResponse, nil
}

func (r *routerRuntime) Reconcile() ([]runtime.InstanceStatus, error) {
	r.reconcileCalls++
	return []runtime.InstanceStatus{r.inspect}, nil
}

func (r *routerRuntime) CleanupInstanceData(instanceID string) error {
	return nil
}

func (r *routerRuntime) CaptureBaseImage(baseImageID, sourceDataDir string) (string, error) {
	return "", nil
}

func (r *routerRuntime) CreateTransportSession(req runtime.TransportSessionRequest) (runtime.TransportSession, error) {
	return runtime.TransportSession{
		SessionID:     req.SessionID,
		AttachmentID:  req.AttachmentID,
		InstanceID:    req.InstanceID,
		RuntimeClass:  req.RuntimeClass,
		Mode:          "prototype-webrtc",
		Status:        "stub",
		WorkerBaseURL: req.WorkerBaseURL,
		SessionToken:  "router-runtime-transport-token",
	}, nil
}

func TestWorkerEndpoints(t *testing.T) {
	handles := runtime.NewMemoryHandleStore()
	androidRuntime := &routerRuntime{
		runtimeClass: "android-studio-emulator",
		displayName:  "Android Studio Emulator",
		templates: []runtime.Template{
			{
				TemplateID:   "android-studio:Medium_Phone",
				RuntimeClass: "android-studio-emulator",
				DisplayName:  "Medium_Phone",
				Metadata: map[string]string{
					"avdName":                 "Medium_Phone",
					"emulatorPath":            "/Applications/Android Studio.app/Contents/MacOS/emulator",
					"hw.lcd.width":            "1080",
					"hw.lcd.height":           "2400",
					"disk.dataPartition.size": "6442450944",
				},
			},
		},
		startResponse: runtime.InstanceHandle{
			RuntimeClass: "android-studio-emulator",
			WorkerID:     "local",
			PID:          4321,
			ConsolePort:  5600,
			AdbPort:      5601,
			GRPCPort:     8554,
		},
		inspect: runtime.InstanceStatus{
			State: "running",
			Handle: runtime.InstanceHandle{
				InstanceID:   "instance-1",
				TemplateID:   "android-studio:Medium_Phone",
				RuntimeClass: "android-studio-emulator",
				WorkerID:     "local",
				PID:          4321,
				ConsolePort:  5600,
				AdbPort:      5601,
				GRPCPort:     8554,
			},
		},
		stopResponse: runtime.InstanceStatus{
			State: "stopped",
			Handle: runtime.InstanceHandle{
				InstanceID:   "instance-1",
				TemplateID:   "android-studio:Medium_Phone",
				RuntimeClass: "android-studio-emulator",
				WorkerID:     "local",
				PID:          4321,
			},
		},
	}
	foreignRuntime := &routerRuntime{
		runtimeClass: "other-runtime",
		displayName:  "Other Runtime",
		inspectErr:   errors.New("foreign adapter should not be inspected"),
		stopErr:      errors.New("foreign adapter should not be stopped"),
	}
	registry := runtime.NewRegistryWithHandles(handles, androidRuntime, foreignRuntime)
	handles.Put(runtime.InstanceHandle{
		InstanceID:   "instance-1",
		TemplateID:   "android-studio:Medium_Phone",
		RuntimeClass: "android-studio-emulator",
		WorkerID:     "local",
		PID:          4321,
		ConsolePort:  5600,
		AdbPort:      5601,
		GRPCPort:     8554,
	})

	router := api.NewRouter(api.Dependencies{
		WorkerID:        "local",
		HostInventory:   api.HostInventory{HostOS: "macos", Arch: "arm64"},
		RuntimeRegistry: registry,
	})

	t.Run("health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var body api.HealthResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if body.Status != "ok" {
			t.Fatalf("expected status ok, got %q", body.Status)
		}
	})

	t.Run("templates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/templates", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var body api.TemplateListResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if len(body.Templates) != 1 {
			t.Fatalf("expected 1 template, got %d", len(body.Templates))
		}

		if body.Templates[0].TemplateID != "android-studio:Medium_Phone" {
			t.Fatalf("unexpected template id %q", body.Templates[0].TemplateID)
		}
	})

	t.Run("start instance", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/instances/start", strings.NewReader(`{
            "instanceId": "instance-1",
            "runtimeClass": "android-studio-emulator",
            "templateId": "android-studio:Medium_Phone",
            "launchConfig": {
                "headless": true,
                "readOnly": true,
                "wipeData": false
            }
        }`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
		}

		var body api.StartInstanceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if body.Handle.PID != 4321 {
			t.Fatalf("expected pid 4321, got %d", body.Handle.PID)
		}

		if body.State != "starting" {
			t.Fatalf("expected starting state, got %q", body.State)
		}
	})

	t.Run("inspect instance", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/v1/instances/instance-1", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var body api.InspectInstanceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if body.Handle.InstanceID != "instance-1" {
			t.Fatalf("expected instance id instance-1, got %q", body.Handle.InstanceID)
		}

		if body.State != "running" {
			t.Fatalf("expected running state, got %q", body.State)
		}
	})

	t.Run("stop instance", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-1/stop", nil)
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", rec.Code)
		}

		var body api.StopInstanceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}

		if body.State != "stopped" {
			t.Fatalf("expected stopped state, got %q", body.State)
		}
		if body.Handle.InstanceID != "instance-1" {
			t.Fatalf("expected instance id instance-1, got %q", body.Handle.InstanceID)
		}
		if androidRuntime.stopCalls != 1 {
			t.Fatalf("expected stop to be called once, got %d", androidRuntime.stopCalls)
		}
	})

	t.Run("adopt instance", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-2/adopt", strings.NewReader(`{
            "handle": {
                "instanceId": "instance-2",
                "templateId": "android-studio:Medium_Phone",
                "runtimeClass": "android-studio-emulator",
                "workerId": "local",
                "pid": 9999,
                "consolePort": 5700,
                "adbPort": 5701,
                "grpcPort": 8650
            }
        }`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d with body %s", rec.Code, rec.Body.String())
		}

		var body api.AdoptInstanceResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("unmarshal response: %v", err)
		}
		if body.Handle.PID != 9999 {
			t.Fatalf("expected pid 9999, got %d", body.Handle.PID)
		}

		adopted, ok := handles.Get("instance-2")
		if !ok {
			t.Fatal("expected instance-2 to be present in the handle store after adopt")
		}
		if adopted.ConsolePort != 5700 || adopted.AdbPort != 5701 {
			t.Fatalf("adopted handle ports = %+v, want console=5700 adb=5701", adopted)
		}
	})

	t.Run("adopt instance rejects mismatched path/body ids", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/v1/instances/instance-3/adopt", strings.NewReader(`{
            "handle": {"instanceId": "instance-other", "runtimeClass": "android-studio-emulator"}
        }`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("expected 400, got %d with body %s", rec.Code, rec.Body.String())
		}
	})
}
