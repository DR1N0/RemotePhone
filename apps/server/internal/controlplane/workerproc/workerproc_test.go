package workerproc

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestSpawnAndWaitHealthySucceedsAgainstFakeWorker(t *testing.T) {
	execer := &FakeExecer{}
	port, err := AllocateLocalPort()
	if err != nil {
		t.Fatalf("AllocateLocalPort: %v", err)
	}

	proc, baseURL, err := Spawn(context.Background(), execer, "fake-remotephone-worker", "instance-1", port, nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Kill()

	if err := WaitHealthy(context.Background(), baseURL, time.Second); err != nil {
		t.Fatalf("WaitHealthy: %v", err)
	}
	if len(execer.Starts) != 1 {
		t.Fatalf("Starts = %d, want 1", len(execer.Starts))
	}
	if envValue(execer.Starts[0].Env, "REMOTEPHONE_WORKER_ID") != "instance-1" {
		t.Errorf("REMOTEPHONE_WORKER_ID = %q, want instance-1", envValue(execer.Starts[0].Env, "REMOTEPHONE_WORKER_ID"))
	}
}

func TestWaitHealthyFailsAfterKill(t *testing.T) {
	execer := &FakeExecer{}
	port, err := AllocateLocalPort()
	if err != nil {
		t.Fatalf("AllocateLocalPort: %v", err)
	}
	proc, baseURL, err := Spawn(context.Background(), execer, "fake-remotephone-worker", "instance-1", port, nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := WaitHealthy(context.Background(), baseURL, time.Second); err != nil {
		t.Fatalf("WaitHealthy (before kill): %v", err)
	}

	if err := Kill(proc, time.Second); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := WaitHealthy(ctx, baseURL, 150*time.Millisecond); err == nil {
		t.Error("WaitHealthy after Kill = nil, want an error (worker should be unreachable)")
	}
}

func TestWaitHealthyFailsWhenHandlerReturnsNon200(t *testing.T) {
	execer := &FakeExecer{Handler: func() http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		})
	}}
	port, err := AllocateLocalPort()
	if err != nil {
		t.Fatalf("AllocateLocalPort: %v", err)
	}
	proc, baseURL, err := Spawn(context.Background(), execer, "fake-remotephone-worker", "instance-1", port, nil)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer proc.Kill()

	if err := WaitHealthy(context.Background(), baseURL, 200*time.Millisecond); err == nil {
		t.Error("WaitHealthy = nil, want an error (handler never returns 200)")
	}
}

func TestAllocateLocalPortReturnsDistinctFreePorts(t *testing.T) {
	a, err := AllocateLocalPort()
	if err != nil {
		t.Fatalf("AllocateLocalPort: %v", err)
	}
	b, err := AllocateLocalPort()
	if err != nil {
		t.Fatalf("AllocateLocalPort: %v", err)
	}
	if a == 0 || b == 0 {
		t.Fatalf("got zero port(s): a=%d b=%d", a, b)
	}
}

func TestKillIsNoopOnAlreadyDeadProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX sleep binary")
	}
	cmd := exec.Command("sleep", "0.01")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	pid := cmd.Process.Pid
	cmd.Wait() // let it exit and be reaped

	proc, err := FromPID(pid)
	if err != nil {
		t.Fatalf("FromPID: %v", err)
	}
	if err := Kill(proc, 100*time.Millisecond); err != nil {
		t.Errorf("Kill(already-dead pid) = %v, want nil (best-effort)", err)
	}
}
