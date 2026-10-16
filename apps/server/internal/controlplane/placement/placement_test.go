package placement

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"mako/internal/controlplane/workerproc"
)

func newTestSelector(execer workerproc.Execer) *Selector {
	return NewSelector(execer, "fake-mako-worker")
}

func TestSelectorValidateAcceptsLocalOnly(t *testing.T) {
	s := newTestSelector(&workerproc.FakeExecer{})
	if err := s.Validate(Policy{Kind: "local-only"}); err != nil {
		t.Errorf("Validate(local-only) = %v, want nil", err)
	}
}

func TestSelectorValidateRejectsUnsupportedKinds(t *testing.T) {
	s := newTestSelector(&workerproc.FakeExecer{})
	for _, kind := range []string{"best-fit", "specific-worker", ""} {
		err := s.Validate(Policy{Kind: kind})
		if !errors.Is(err, ErrUnsupported) {
			t.Errorf("Validate(%q) error = %v, want ErrUnsupported", kind, err)
		}
	}
}

func TestAcquireWorkerReturnsHealthyBaseURLAndPID(t *testing.T) {
	execer := &workerproc.FakeExecer{}
	s := newTestSelector(execer)

	baseURL, pid, err := s.AcquireWorker(context.Background(), "instance-1")
	if err != nil {
		t.Fatalf("AcquireWorker: %v", err)
	}
	if baseURL == "" {
		t.Error("AcquireWorker baseURL is empty")
	}
	if pid == 0 {
		t.Error("AcquireWorker pid is zero")
	}
	if len(execer.Starts) != 1 {
		t.Fatalf("Starts = %d, want 1", len(execer.Starts))
	}

	if err := s.ReleaseWorker(context.Background(), pid); err != nil {
		t.Errorf("ReleaseWorker: %v", err)
	}
}

func TestAcquireWorkerKillsProcessIfNeverHealthy(t *testing.T) {
	// A handler that never answers /healthz with 200 simulates a worker
	// process that started but never became reachable.
	execer := &workerproc.FakeExecer{Handler: func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
	}}
	s := newTestSelector(execer)

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, _, err := s.AcquireWorker(ctx, "instance-1")
	if err == nil {
		t.Fatal("AcquireWorker = nil error, want failure (worker never became healthy)")
	}
}

func TestReleaseWorkerIsNoopOnUnknownPID(t *testing.T) {
	s := newTestSelector(&workerproc.FakeExecer{})
	// An arbitrary high PID almost certainly doesn't correspond to a real
	// process on this host — ReleaseWorker must still not error.
	if err := s.ReleaseWorker(context.Background(), 999999); err != nil {
		t.Errorf("ReleaseWorker(unknown pid) = %v, want nil (best-effort)", err)
	}
}
