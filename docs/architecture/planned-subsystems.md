# Planned subsystems (designed, not yet built)

This captures the design intent for subsystems that don't exist in code yet, so the rationale survives even though the original design document it came from has been retired. Unlike the rest of `docs/architecture/`, this file describes a **target**, not current reality — each section should move to `overview.md`/`data-model.md`/`security-model.md`/`api-surfaces.md` (and be deleted from here) once actually implemented, and should be revised in place if the target itself changes.

> **Status 2026-10-22:** the worker lifecycle, controller, and the core of the networking sections below are now implemented — see `overview.md`, `decisions-log.md`, and `docs/plans/worker-lifecycle.md`. This file is kept as design rationale, not a to-do list. Still genuinely unbuilt: pooled/remote workers (and the `workers` table that would reintroduce), WebRTC interface filtering/UDP port ranges, TURN/NAT traversal, and pairing.

## Worker lifecycle: one ephemeral OS process per running instance, not a shared pool

`remotephone-worker` must be spawned and supervised by the control plane (via `exec.Command`, port assigned at spawn time), not called in-process as `worker.NewWorker(...)` from a controller goroutine — even though the worker has no cgo/`unsafe` and so wouldn't risk a native crash taking down the control plane.

**Revised 2026-10-14: one worker process per running instance, not a shared pool of long-lived workers.** Earlier framing assumed a persistent `workers` table and a pool several instances could be placed across. That's dropped — see `decisions-log.md`. The model is now: when an instance starts, the control plane spawns a fresh `remotephone-worker` process for *that instance only*; when the instance stops, that worker process is torn down with it. A single worker process already supports managing more than one instance internally (`internal/worker/runtime/registry.go`'s `handles map[string]InstanceHandle`), but nothing should rely on that — the control plane always spawns 1:1.

The reason for a separate process at all (vs. an in-process goroutine) is still restart semantics: an in-process goroutine would mean every control-plane restart or version bump kills every active WebRTC session simultaneously. A separate process means a control-plane crash/restart doesn't kill the worker — it's reparented to init like any orphan and keeps serving already-negotiated WebRTC media directly to its client (that path never goes through the control plane). The 1:1 model makes failure handling simpler than a pool would: if an instance's worker process crashes, only that one instance's session drops; the liveness probe detects the failed health-poll, respawns a fresh worker for that instance, and the new process reconciles the still-running emulator by its known console/adb port. There's no "which of this worker's several instances need reconciling" question, because there's exactly one. A worker *binary* version bump requires stopping and respawning that instance's worker (interrupting its session) — a deliberate, scoped admin action, not an incidental side effect of every control-plane deploy.

The currently-serving worker's connection info (pid, port, base URL) is **runtime status, not durable config** — it belongs in `instances.observed_state`/`runtime_handle_json` (already an existing spec/status split), updated each time a worker is (re)spawned for that instance. No separate table or column tracks it.

`cmd/remotephone-worker/main.go` should stay a thin wrapper around a `worker.NewWorker(ctx, cfg)`-style constructor. A future pooled/remote worker (one process legitimately serving multiple instances, not controlled by local `exec.Command`) would be a different, additive code path — not a redesign of this one — and is the point at which a `workers` registry table would come back.

## Controller: one controller, two reconcile probes, shared workqueue

The idle-TTL/owner-absence reconciler and worker-liveness management are **one controller, not two** — both are background convergence loops reading/writing shared state; treating them as two probes inside a single controller avoids duplicating queue/resync/shutdown plumbing. `k8s.io/client-go/util/workqueue` (already a dependency) is the queue implementation — rate-limited exponential-backoff retry and dedup for free, not worth hand-rolling.

```go
type Reconciler interface { Reconcile(ctx context.Context, id string) error }

type Controller struct {
    queue        workqueue.RateLimitingInterface // keyed "instance:<id>" / "session:<id>"
    workerProbe  Reconciler // liveness: health-poll each running instance's worker process; respawn on failure and reconcile with its still-running emulator
    sessionProbe Reconciler // owner-absence / idle-TTL teardown
}
```

This matches the skeleton already in `internal/controlplane/controller/controller.go` (the `Reconciler` interface and dispatch-by-key-prefix exist) — what's missing is real `workerProbe`/`sessionProbe` implementations. Under the 1:1 ephemeral-worker model (see above), `workerProbe` is keyed by instance ID, not by an independent worker ID — there is no separate worker identity to dispatch on.

`apiserver` should hold a reference to a narrow `Enqueue` interface only (`EnqueueInstance(id)` / `EnqueueSession(id)`), never to `Reconcile` or the probes directly — e.g. "last attachment disconnects" or "admin force-stops an instance" enqueues inline from the HTTP handler for an immediate reconcile, instead of waiting for the controller's periodic full resync. Dependency direction is one-way: `apiserver` imports `controller`'s enqueue interface; `controller` never imports `apiserver`.

`placement/` stays on the request-driven (`apiserver`) side, not inside `controller` — under the 1:1 model it has no pool to select among; its job at instance-creation time is just to allocate a port and invoke the spawn, a one-shot action, not a reconcile loop.

## Networking: three distinct traffic paths

1. **Control-plane client API** — signaling only (login, session management, SDP offer/answer, ICE candidate exchange as JSON). The only port carrying auth material. HTTPS, trusted through a single-use access code and certificate pinning (see `security-model.md`).
2. **Worker media/data ports** — a separate port from the worker's own internal control API. The worker's pion WebRTC stack should bind to the host's real LAN interface (not loopback) so a different-device client can reach it, and should call `SetEphemeralUDPPortRange(min, max)` so ports are bounded instead of fully arbitrary (matters once a host firewall or future NAT/TURN work needs predictable ranges). This is the mechanism that makes "control-plane restart doesn't drop active streams" true — the client connects directly to the worker's negotiated ICE candidates, never through the control plane.
3. **Worker internal control API** (control-plane → worker instance/transport calls) — bind strictly to `127.0.0.1`. Nothing outside the host, including any client, ever needs to reach it.

No public-internet or NAT-traversal goal exists — this is a trusted-LAN deployment. Every device on the LAN can already reach every other port on the host, so there's no "expose only one port" constraint to design around.

The admin/client HTTP-surface split (loopback-by-default `adminapi` vs. LAN-reachable `clientapi`) should be kept distinct even once both terminate HTTPS and require login — they differ in *network exposure*, not just authentication. `adminapi`'s loopback binding is an independent barrier that a stolen bearer token or a missed authorization check can't bypass, since the attacker would also need actual host access. Where remote admin convenience is wanted, the fix is making `adminapi`'s bind address configurable (default loopback, opt-in LAN) — not merging the two surfaces onto one port.

## Error handling and testing conventions (for not-yet-built packages)

Follow the pattern already established in `internal/worker/api/router.go`: sentinel errors checked with `errors.Is`, mapped to HTTP status at the route-handler boundary. `apiserver` route tests should use `httptest`; `controller` tests should use a fake clock and a fake process-execer so unit tests never shell out to a real `remotephone-worker` binary, with one integration test spawning a real worker process to exercise start/stop/transport end to end.

## Verification criteria for this set of subsystems, once built

- `clientapi` never exposes `/admin/*`, base-image promote, or any cross-owner instance operation — a stolen admin bearer token used against `clientapi` gets only the admin's own owner-scoped data.
- Pairing: a changed server key, wrong PIN, or malformed trust state is rejected before any password/Authorization leaves the client; a same-key certificate renewal (via SPKI pinning) doesn't break an already-paired client.
- Controller: killing an instance's worker process triggers detection, respawn, and reconciliation of its still-running emulator within one resync interval, without affecting any other instance's worker; killing and restarting the control-plane process itself does not interrupt an already-negotiated WebRTC session.
