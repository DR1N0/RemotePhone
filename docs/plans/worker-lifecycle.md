# Phase 4: Worker lifecycle

**Status: Done.** Verified by unit tests (fake `Execer`/HTTP-backed fake workers throughout — `workerproc`, `placement`, `instance`, `transport`, `reconcile` all exercised without shelling out) and a real end-to-end run against the actual `remotephone-worker` binary and a real Android Studio Emulator (`Medium_Phone_API_36.1`): create → start (spawned a real `remotephone-worker` OS process, which booted a real `qemu-system-aarch64` emulator as its child) → `kill -9` the worker process → the liveness reconciler detected the failed health-check within one resync tick, spawned a replacement worker (new PID, new port), and the replacement adopted the still-running emulator via `POST /v1/instances/{id}/adopt` — confirmed by the emulator's PID/console/adb/grpc ports staying identical across the respawn while `workerPid`/`workerBaseUrl` changed, and the emulator process never restarting — then stop cleanly shut down both the worker and the emulator. One real bug this surfaced and fixed: `placement.Selector.AcquireWorker` was spawning workers with `os.Environ()` missing entirely (only the 3 `REMOTEPHONE_WORKER_*` vars), so a real spawn had no `PATH`/`ANDROID_SDK_ROOT` and silently failed template/instance discovery — passing `os.Environ()` as the base env fixed it.

Living plan — edit in place as scope evolves; not dated/immutable. See `roadmap.md` for how this fits the overall sequence.

**Goal:** real `exec.Command` spawning — one `remotephone-worker` process per running instance, health-checked and respawned on failure, no placement/affinity logic (strict 1:1 model). No TS precedent exists for this phase; the target design was written down in advance in `docs/architecture/planned-subsystems.md`'s "Worker lifecycle" and "Controller" sections and `decisions-log.md` (2026-10-14) — this plan executes that design, including one addition it didn't call out explicitly: a worker-side "adopt" capability, without which "respawn and reconcile with the still-running emulator" has nothing to call.

**Depends on:** instance (Phase 1), session & transport (Phase 2), admin & reconciliation (Phase 3, for the `controller`/`reconcile` packages this phase plugs real probes into).

## Scope

### Worker binary (`internal/worker/`)
- `runtime.Registry.Adopt(handle InstanceHandle) error` — inserts a caller-supplied handle directly into the `HandleStore`, no launch. Needed because a freshly-spawned replacement worker process starts with an empty in-memory `HandleStore` and otherwise has no way to discover an emulator it didn't itself launch (confirmed: `Registry.Reconcile()` only re-validates handles *already* in its own store via PID-liveness, it can't rediscover one).
- `runtime.InstanceHandle` gets two new fields: `WorkerPID int`, `WorkerBaseURL string` — distinct from the existing emulator-process `PID`/`ConsolePort`/`AdbPort`/`GRPCPort`. No migration: `instances.runtime_handle_json` already stores this whole struct.
- New route `POST /v1/instances/{id}/adopt` on `internal/worker/api`, mirroring `/v1/instances/start`'s validation style, mapping to `Registry.Adopt`.

### New package: `internal/controlplane/workerproc`
OS-process mechanics only (spawn/health-check/kill), kept separate from `placement` so the `exec.Command` seam is small and fake-able in tests — mirrors how `instance.WorkerClient` is already a narrow interface with a real and a fake implementation. `Execer`/`Process` interfaces (real `OSExecer` wraps `os/exec`), `Spawn`, `WaitHealthy` (polls `GET /healthz`), `AllocateLocalPort` (bind `:0`, read back the port), `Kill`.

### `internal/controlplane/placement`
`Selector` keeps its existing `Validate` (still only accepts `"local-only"`) and grows `AcquireWorker(ctx, instanceID) (baseURL string, pid int, err error)` / `ReleaseWorker(ctx, pid int) error`, built on `workerproc`. `NewSelector` signature changes to take an `Execer` and a worker binary path — every test call site across `instance`/`transport`/`admin`/`adminapi`/`reconcile` that constructs `placement.NewSelector()` needs updating.

### `instance` and `transport` packages
Today both hold one shared `workerClient WorkerClient` field pointed at a single static `REMOTEPHONE_WORKER_BASE_URL` — the assumption this whole phase breaks. Replaced with: resolve a `WorkerClient` from the specific instance's persisted `RuntimeHandle.WorkerBaseURL` (`instance.Service.WorkerClientFor`, exported so `transport.Service` uses it too instead of holding its own client). `StartManagedInstance` acquires a worker before calling `StartInstance`; `StopManagedInstance` releases it after `StopInstance`. New `ListRunningInstanceIDs` for the controller's resync ticker (same shape as Phase 3's `ListExpiredOwnerAbsenceSessionIDs`).

### `reconcile.WorkerLivenessProbe`
New sibling to Phase 3's `OwnerAbsenceProbe`, replacing `controller.NoopReconciler{}` as the `workerProbe`. Keyed by instance ID (no rename of `controller.go`'s `"worker:"` prefix / `EnqueueWorker` needed — it was always just a string key). `Reconcile(ctx, instanceID)`: no-op if the instance isn't desired-running; health-check its worker; if unreachable, best-effort-kill the old PID, spawn a fresh worker, call its new `AdoptInstance` (→ `POST /v1/instances/{id}/adopt`) with the persisted emulator handle fields, persist the updated worker PID/base URL, audit-record the respawn.

### `main.go`
New `REMOTEPHONE_WORKER_BINARY_PATH` env var (defaults to the sibling path of the running `remotephone-controlplane` binary); real `workerProbe`; resync ticker extended to also enqueue every running instance ID each tick.

## Testing

Per `decisions-log.md`'s bar for this phase: unit tests throughout use a fake clock / fake `Execer` so they never shell out to a real binary (`workerproc`, `placement`, `reconcile.WorkerLivenessProbe` all get fakes; `instance`/`transport` inject a fake `WorkerAcquirer` instead of a real `Selector`). One real end-to-end run against the actual `remotephone-worker` binary and a real Android Studio Emulator (available in this environment at `$HOME/Library/Android/sdk/emulator/emulator`) verifies: create → start → kill the worker process → liveness probe respawns a new worker → the still-running emulator is adopted without a reboot → session/transport keep working.

## Explicitly out of scope for this phase

- Any shared/pooled worker model — stays strictly 1:1.
- A `workers` table — PID/base URL stay in `runtime_handle_json`, no new schema.
- Placement/affinity selection among multiple hosts.
- Pairing/TLS (Phase 5), base images/templates (Phase 6).
