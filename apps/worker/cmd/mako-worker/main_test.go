package main

import (
	"errors"
	"testing"

	"mako-worker/internal/config"
	workerruntime "mako-worker/internal/runtime"
	"mako-worker/internal/runtime/androidstudio"
)

type stubRuntimeAdapter struct {
	runtimeClass string
	inspect      workerruntime.InstanceStatus
	inspectErr   error
	inspectCalls int
	stop         workerruntime.InstanceStatus
	stopErr      error
	stopCalls    int
}

func (s *stubRuntimeAdapter) RuntimeClass() string {
	return s.runtimeClass
}

func (s *stubRuntimeAdapter) DisplayName() string {
	return s.runtimeClass
}

func (s *stubRuntimeAdapter) DiscoverTemplates() ([]workerruntime.Template, error) {
	return nil, nil
}

func (s *stubRuntimeAdapter) Start(workerruntime.StartRequest) (workerruntime.InstanceHandle, error) {
	return workerruntime.InstanceHandle{}, nil
}

func (s *stubRuntimeAdapter) Inspect(instanceID string) (workerruntime.InstanceStatus, error) {
	s.inspectCalls++
	if s.inspectErr != nil {
		return workerruntime.InstanceStatus{}, s.inspectErr
	}

	status := s.inspect
	if status.Handle.InstanceID == "" {
		status.Handle.InstanceID = instanceID
	}
	return status, nil
}

func (s *stubRuntimeAdapter) Stop(instanceID string) (workerruntime.InstanceStatus, error) {
	s.stopCalls++
	if s.stopErr != nil {
		return workerruntime.InstanceStatus{}, s.stopErr
	}

	status := s.stop
	if status.Handle.InstanceID == "" {
		status.Handle.InstanceID = instanceID
	}
	return status, nil
}

func (s *stubRuntimeAdapter) Reconcile() ([]workerruntime.InstanceStatus, error) {
	return nil, nil
}

func (s *stubRuntimeAdapter) CreateTransportSession(req workerruntime.TransportSessionRequest) (workerruntime.TransportSession, error) {
	return workerruntime.TransportSession{
		SessionID:     req.SessionID,
		AttachmentID:  req.AttachmentID,
		InstanceID:    req.InstanceID,
		RuntimeClass:  req.RuntimeClass,
		Mode:          "prototype-webrtc",
		Status:        "stub",
		WorkerBaseURL: req.WorkerBaseURL,
		SessionToken:  "stub-runtime-token",
	}, nil
}

func TestNewRuntimeRegistryUsesHandleStoreForInspectRouting(t *testing.T) {
	handles := workerruntime.NewMemoryHandleStore()
	handles.Put(workerruntime.InstanceHandle{
		InstanceID:   "instance-1",
		RuntimeClass: "z-android",
	})

	android := &stubRuntimeAdapter{
		runtimeClass: "z-android",
		inspect: workerruntime.InstanceStatus{
			State: "running",
			Handle: workerruntime.InstanceHandle{
				InstanceID:   "instance-1",
				RuntimeClass: "z-android",
			},
		},
	}
	foreign := &stubRuntimeAdapter{
		runtimeClass: "a-foreign",
		inspectErr:   errors.New("foreign adapter should not be inspected"),
	}

	registry := newRuntimeRegistry(handles, android, foreign)
	status, err := registry.Inspect("instance-1")
	if err != nil {
		t.Fatalf("inspect instance: %v", err)
	}

	if status.Handle.RuntimeClass != "z-android" {
		t.Fatalf("expected inspect result from z-android adapter, got %q", status.Handle.RuntimeClass)
	}
	if android.inspectCalls != 1 {
		t.Fatalf("expected android adapter inspect to be called once, got %d", android.inspectCalls)
	}
	if foreign.inspectCalls != 0 {
		t.Fatalf("expected foreign adapter inspect to be skipped, got %d calls", foreign.inspectCalls)
	}
}

func TestBuildStartupRuntimeRegistrySkipsAndroidStudioWhenUnavailable(t *testing.T) {
	handles := workerruntime.NewMemoryHandleStore()
	cfg := config.Config{
		WorkerID:            "local",
		HomeDir:             t.TempDir(),
		AndroidEmulatorPath: "/path/that/does/not/exist",
	}

	registry := buildStartupRuntimeRegistry(cfg, handles, nil)
	if _, ok := registry.Get(androidstudio.RuntimeClass); ok {
		t.Fatalf("expected %q adapter to be absent when emulator is unavailable", androidstudio.RuntimeClass)
	}
	if adapters := registry.ListAdapters(); len(adapters) != 0 {
		t.Fatalf("expected no registered adapters when emulator is unavailable, got %d", len(adapters))
	}
}
