// Package workerproc owns the OS-process mechanics of running a
// mako-worker binary: starting it, waiting for it to become reachable, and
// killing it. It is deliberately narrow and kept separate from
// internal/controlplane/placement (which owns the policy — when to spawn,
// which port to use) so the exec.Command seam is small and fake-able in
// tests, per docs/architecture/decisions-log.md's bar for this phase
// ("a fake process-execer so unit tests never shell out to a real
// mako-worker binary"). See docs/plans/worker-lifecycle.md.
package workerproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// Process is a running OS process — the minimal surface this package and
// its callers need.
type Process interface {
	PID() int
	Kill() error
	Wait() error
}

// Execer starts a new OS process. OSExecer is the real implementation;
// FakeExecer (fake.go) is the test double.
type Execer interface {
	Start(ctx context.Context, binaryPath string, env []string) (Process, error)
}

// OSExecer is the real Execer, wrapping os/exec. Deliberately does NOT use
// exec.CommandContext: a spawned mako-worker must keep running independently
// of the request that spawned it (even across a control-plane restart) —
// see docs/architecture/planned-subsystems.md's "Worker lifecycle" section.
type OSExecer struct{}

type osProcess struct {
	cmd *exec.Cmd
}

func (p *osProcess) PID() int    { return p.cmd.Process.Pid }
func (p *osProcess) Kill() error { return p.cmd.Process.Kill() }
func (p *osProcess) Wait() error { return p.cmd.Wait() }

func (OSExecer) Start(ctx context.Context, binaryPath string, env []string) (Process, error) {
	cmd := exec.Command(binaryPath)
	cmd.Env = env
	// A crash log on disk is more useful than output silently mixed into
	// (or lost from) the control plane's own stdout/stderr.
	if logFile, err := os.CreateTemp("", "mako-worker-*.log"); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	} else {
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start worker process: %w", err)
	}
	return &osProcess{cmd: cmd}, nil
}

// Spawn starts a fresh mako-worker process bound to 127.0.0.1:listenPort,
// labeled instanceID (MAKO_WORKER_ID — matches the 1:1 model: one worker
// process per running instance), and returns its handle. The process is
// not necessarily healthy yet — callers must WaitHealthy next.
func Spawn(ctx context.Context, execer Execer, binaryPath, instanceID string, listenPort int, extraEnv []string) (Process, string, error) {
	addr := fmt.Sprintf("127.0.0.1:%d", listenPort)
	baseURL := "http://" + addr
	env := append(append([]string{}, extraEnv...),
		"MAKO_WORKER_LISTEN_ADDR="+addr,
		"MAKO_WORKER_BASE_URL="+baseURL,
		"MAKO_WORKER_ID="+instanceID,
	)
	proc, err := execer.Start(ctx, binaryPath, env)
	if err != nil {
		return nil, "", err
	}
	return proc, baseURL, nil
}

// WaitHealthy polls baseURL's /healthz until it answers 200 or timeout
// elapses.
func WaitHealthy(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 2 * time.Second}
	var lastErr error
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/healthz", nil)
		if err != nil {
			return fmt.Errorf("build healthz request: %w", err)
		}
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("healthz returned status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("worker at %s did not become healthy within %s: %w", baseURL, timeout, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// AllocateLocalPort binds to 127.0.0.1:0, reads back the OS-assigned free
// port, and releases it immediately — the standard Go idiom for "give me a
// free port." Used for the worker's own HTTP listen port, distinct from
// internal/worker/host.AllocateEmulatorPorts (which allocates the
// emulator's console/adb/grpc ports, inside the worker process itself).
func AllocateLocalPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("allocate local port: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// pidProcess wraps a bare *os.Process reconstructed from a persisted PID —
// NOT necessarily a child of this OS process (e.g. after a control-plane
// restart), so Wait() can't block on OS-level child-reaping the way
// osProcess's can. Kill() is all a release-by-PID can really promise.
type pidProcess struct {
	proc *os.Process
}

func (p *pidProcess) PID() int    { return p.proc.Pid }
func (p *pidProcess) Kill() error { return p.proc.Kill() }
func (p *pidProcess) Wait() error { _, _ = p.proc.Wait(); return nil }

// FromPID reconstructs a Process from a bare PID — for releasing a worker
// this process didn't itself Spawn (e.g. after a control-plane restart,
// where only the PID survives, persisted in instances.runtime_handle_json).
// Always succeeds on POSIX (os.FindProcess does no liveness check); a
// stale/already-dead PID surfaces as a harmless no-op from Kill, not an
// error here.
func FromPID(pid int) (Process, error) {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil, err
	}
	return &pidProcess{proc: proc}, nil
}

// Kill sends a kill signal and waits (briefly) for exit. Best-effort: a
// process that's already gone is a success, not an error — every caller of
// Kill means "make sure this is gone," never "assert it was alive."
func Kill(proc Process, timeout time.Duration) error {
	if err := proc.Kill(); err != nil {
		if errors.Is(err, os.ErrProcessDone) || errors.Is(err, syscall.ESRCH) {
			return nil
		}
		return fmt.Errorf("kill worker process: %w", err)
	}
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
	}
	return nil
}
