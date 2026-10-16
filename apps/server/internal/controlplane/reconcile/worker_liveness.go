package reconcile

import (
	"context"
	"errors"
	"fmt"
	"time"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/workerproc"

	"github.com/google/uuid"
)

// workerHealthCheckTimeout bounds one liveness probe's single health check —
// short, since this runs every resync tick for every running instance, not
// a one-shot wait like placement.Selector.AcquireWorker's.
const workerHealthCheckTimeout = 3 * time.Second

// WorkerLivenessProbe implements controller.Reconciler (as the workerProbe
// passed to controller.New) for running instances' worker processes. Keyed
// by instance ID — see docs/plans/worker-lifecycle.md for why there's no
// separate worker identity to dispatch on under the 1:1 ephemeral-worker
// model. Reconcile is always safe to call speculatively: it no-ops whenever
// the instance isn't desired-running or has no worker recorded yet (e.g.
// mid-start).
type WorkerLivenessProbe struct {
	instanceSvc       *instance.Service
	placementSelector instance.PlacementSelector
}

func NewWorkerLivenessProbe(instanceSvc *instance.Service, placementSelector instance.PlacementSelector) *WorkerLivenessProbe {
	return &WorkerLivenessProbe{instanceSvc: instanceSvc, placementSelector: placementSelector}
}

// Reconcile health-checks instanceID's worker; if it's unreachable, kills
// the old process (best-effort — it may just be hung, not dead), spawns a
// replacement, has the replacement adopt the still-running emulator (no
// reboot) via AdoptInstance, and persists the new worker PID/base URL.
func (p *WorkerLivenessProbe) Reconcile(ctx context.Context, instanceID string) error {
	inst, err := p.instanceSvc.GetManagedInstanceUnchecked(ctx, instanceID)
	if err != nil {
		if errors.Is(err, instance.ErrNotFound) {
			return nil
		}
		return err
	}
	if inst.DesiredState != "running" {
		return nil
	}
	if inst.RuntimeHandle == nil || inst.RuntimeHandle.WorkerBaseURL == "" {
		return nil
	}

	if err := workerHealthCheck(ctx, inst.RuntimeHandle.WorkerBaseURL); err == nil {
		return nil
	}

	if inst.RuntimeHandle.WorkerPID != 0 {
		_ = p.placementSelector.ReleaseWorker(ctx, inst.RuntimeHandle.WorkerPID)
	}
	baseURL, pid, err := p.placementSelector.AcquireWorker(ctx, instanceID)
	if err != nil {
		return fmt.Errorf("acquire replacement worker: %w", err)
	}

	newHandle := *inst.RuntimeHandle
	newHandle.WorkerPID = pid
	newHandle.WorkerBaseURL = baseURL
	workerClient := instance.NewHTTPWorkerClient(baseURL)
	if err := workerClient.AdoptInstance(ctx, instanceID, newHandle); err != nil {
		return fmt.Errorf("adopt instance on replacement worker: %w", err)
	}

	actor := audit.NewContext(nil, nil, uuid.NewString())
	if _, err := p.instanceSvc.UpdateRuntimeHandle(ctx, instanceID, newHandle, actor); err != nil {
		return fmt.Errorf("persist respawned worker handle: %w", err)
	}
	return nil
}

func workerHealthCheck(ctx context.Context, baseURL string) error {
	// workerproc.WaitHealthy polls until healthy or timeout; a short
	// timeout here makes it behave as a single check — on the resync
	// cadence, "still unhealthy this tick" just means try again next tick,
	// not block the whole reconcile loop waiting.
	return workerproc.WaitHealthy(ctx, baseURL, workerHealthCheckTimeout)
}
