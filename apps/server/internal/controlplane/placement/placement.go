package placement

import (
	"context"
	"fmt"
	"os"
	"time"

	"mako/internal/controlplane/workerproc"
)

// workerHealthyTimeout bounds how long AcquireWorker waits for a freshly
// spawned mako-worker process to answer /healthz before giving up.
const workerHealthyTimeout = 10 * time.Second

// workerKillTimeout bounds how long ReleaseWorker waits for a killed worker
// process to actually exit before moving on — best-effort, not a hard
// requirement (see workerproc.Kill's doc comment).
const workerKillTimeout = 5 * time.Second

// ErrUnsupported is returned when a Policy names a kind Selector can't
// honor.
var ErrUnsupported = fmt.Errorf("placement policy not supported")

// Policy is the control-plane request-level placement policy a
// CreateInstanceRequest carries — the worker itself never sees this.
type Policy struct {
	Kind     string `json:"kind"`
	WorkerID string `json:"workerId,omitempty"`
}

// Selector validates that a requested placement policy can actually be
// honored, and (from Phase 4 on) owns spawning/releasing the one
// mako-worker process a running instance gets. Still no pool to select
// among — every instance's worker is spawned fresh, 1:1 — so Validate still
// only ever accepts "local-only", matching the TS placement-service.ts's
// own "local-only, or reject" behavior. See docs/architecture/decisions-log.md
// (2026-10-14): the 1:1 ephemeral worker-per-instance model has no
// placement/affinity problem to solve, and docs/plans/worker-lifecycle.md
// for why spawning lives here rather than in instance.Service directly.
type Selector struct {
	execer           workerproc.Execer
	workerBinaryPath string
}

func NewSelector(execer workerproc.Execer, workerBinaryPath string) *Selector {
	return &Selector{execer: execer, workerBinaryPath: workerBinaryPath}
}

// Validate reports ErrUnsupported unless policy's kind is "local-only" —
// the only kind any phase before a real worker pool exists can honor.
func (s *Selector) Validate(policy Policy) error {
	if policy.Kind != "local-only" {
		return fmt.Errorf("%w: %q", ErrUnsupported, policy.Kind)
	}
	return nil
}

// AcquireWorker allocates a free local port, spawns a fresh mako-worker
// process for instanceID, and waits for it to answer healthy before
// returning its base URL and PID. On any failure after a process is
// actually spawned, it is killed before returning the error — a failed
// acquisition never leaks a running worker process.
func (s *Selector) AcquireWorker(ctx context.Context, instanceID string) (baseURL string, pid int, err error) {
	port, err := workerproc.AllocateLocalPort()
	if err != nil {
		return "", 0, fmt.Errorf("allocate worker port: %w", err)
	}
	// os.Environ(), not nil: workerproc.Spawn's env slice becomes the
	// child's ENTIRE environment (exec.Cmd.Env semantics), so the control
	// plane's own PATH/HOME/MAKO_ANDROID_SDK_ROOT/etc. must be passed
	// through explicitly or the spawned mako-worker starts with none of
	// them — e.g. unable to find the emulator binary or any AVD at all.
	proc, baseURL, err := workerproc.Spawn(ctx, s.execer, s.workerBinaryPath, instanceID, port, os.Environ())
	if err != nil {
		return "", 0, fmt.Errorf("spawn worker: %w", err)
	}
	if err := workerproc.WaitHealthy(ctx, baseURL, workerHealthyTimeout); err != nil {
		_ = workerproc.Kill(proc, workerKillTimeout)
		return "", 0, fmt.Errorf("wait for worker healthy: %w", err)
	}
	return baseURL, proc.PID(), nil
}

// ReleaseWorker kills the worker process identified by pid — reconstructed
// from the bare PID (workerproc.FromPID) since the original in-memory
// Process handle from AcquireWorker may be long gone (e.g. across a
// control-plane restart); only the PID is persisted
// (instances.runtime_handle_json). Best-effort: a PID that's already dead
// is not an error (see workerproc.Kill).
func (s *Selector) ReleaseWorker(ctx context.Context, pid int) error {
	proc, err := workerproc.FromPID(pid)
	if err != nil {
		return fmt.Errorf("reconstruct worker process %d: %w", pid, err)
	}
	return workerproc.Kill(proc, workerKillTimeout)
}
