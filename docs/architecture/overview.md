# System overview

This document describes the mako control plane **as it actually exists right now**. Unlike design/planning documents, this file is edited in place as reality changes — if it disagrees with the code, the code wins and this file is wrong.

## Motivation (why this exists instead of Moonlight/Sunshine/Apollo/Artemis)

The project started from two structural limits hit while using Artemis+Apollo (a Moonlight/Sunshine-family streaming pair) to play touch-native mobile games on a phone over a streamed PC session:

1. **No real touch semantics.** Desktop OSes have no native touchscreen concept; Apollo can only translate touch gestures into a virtual gamepad/mouse, which doesn't match games actually designed for touch input.
2. **Single input/output, no real multi-instance concurrency.** A physical host's keyboard/mouse/display are exclusive resources — two clients can never each independently drive two separate "instances" on the same host. That's the Windows/macOS multi-seat-input problem, not a gap in Apollo's implementation.

An Android emulator is touch-first by construction, and running several independent instances on one host is already an ordinary, solved capability (Android Studio runs multiple AVDs in parallel routinely). Wrapping each instance's full audio/video/touch I/O in WebRTC sidesteps the multi-seat problem entirely, rather than patching around it at the desktop-streaming layer. This is why mako is scoped to Android emulator instances specifically, not general desktop/game streaming — see `decisions-log.md` (2026-10-15) for the full discussion, including why this means mako isn't actually competing with desktop-streaming tools or general remote-desktop/cloud-gaming products (e.g. 网易UU远程) on "best streaming experience" — those solve a different problem than the one mako exists for.

## Status

- **`apps/server` (Go) is the active implementation.** One Go module (`module mako`, Go 1.27.2), two binaries: `cmd/mako-controlplane` and `cmd/mako-worker`. Auth, instance CRUD/lifecycle, session/transport (including signaling and PIN-based invites), admin/reconciliation (user management, audit-log retrieval, cross-owner instance listing, an owner-absence reconciler), worker lifecycle (real `exec.Command` spawning, health-checking, crash respawn with emulator adoption), and the client surface's self-signed TLS identity are fully ported and routed. Image and pairing (PIN-confirmed client trust) are still empty stub packages — pairing deliberately so, deferred until a native client exists to pair with (see `security-model.md`).
- **`apps/controlplane` (TypeScript) is legacy**, last touched 2026-10-08. It is the reference implementation the Go port is being built from, not the running system going forward. It has not been deleted because large parts of its behavior (instance/session/admin/audit services) haven't been ported yet and it's still read as a behavioral reference.
- **There is no separate `apps/worker` directory.** The old standalone Go worker was folded into `apps/server` as `internal/worker/` (subpackages: `api`, `config`, `host`, `runtime`, `store`, `tests`, `transport`), compiled into the `cmd/mako-worker` binary.
- **There is no browser-based streaming client, and none is planned.** Early design iterations (mid-2026) built a React/antd admin UI with a live `<video>` session page talking WebRTC directly to the worker. That page was progressively demoted to an internal diagnostic and is not part of the product surface. The only thing that ever establishes a streaming session against a worker is the future native `mako-client` app, over a trusted LAN. Don't resurrect browser-streaming assumptions when reading old commit history or TS code — treat any TS code implementing a browser `<video>` session page as dead weight, not a spec to port.

## Process topology

Two OS processes, same host, same Go module:

- **`mako-controlplane`** — the orchestrator: auth, sessions, instances, admin API, client API, placement, and (eventually) worker lifecycle management.
- **`mako-worker`** — a standalone process. The control plane spawns one ephemeral worker process **per running instance** (not a shared pool) via `internal/controlplane/workerproc` (the `exec.Command` mechanics) and `internal/controlplane/placement` (port allocation + when to spawn/release) — see `docs/architecture/decisions-log.md` (2026-10-14) and `planned-subsystems.md` for why. There is no single static `MAKO_WORKER_BASE_URL` anymore; every worker interaction resolves a client from the specific instance's persisted `runtime_handle_json` (`instance.Service.WorkerClientFor`). `internal/controlplane/controller/controller.go`'s workqueue-based `Controller` runs both probes for real: `reconcile.OwnerAbsenceProbe` (session probe) and `reconcile.WorkerLivenessProbe` (worker probe) — the latter health-checks each running instance's worker and, on failure, kills the old process, spawns a replacement, and has it adopt the still-running emulator via the worker's `POST /v1/instances/{id}/adopt` route (no reboot). Both probes are periodically resynced from `cmd/mako-controlplane/main.go`. The worker is kept as a separate OS process (not an in-process goroutine) specifically so a control-plane restart doesn't kill active WebRTC sessions, and so a worker crash doesn't kill the emulator it launched (plain `exec.Command`, no process-group/`Pdeathsig` wiring — children are reparented to init, not killed, when their parent dies).

Both binaries share one module so `internal/` packages are trivially shared with one `go.sum`. Folder convention substitutes for a module boundary: `internal/contracts/` (not yet created) is meant to be the only package both binaries import; `internal/controlplane/` and `internal/worker/` hold everything specific to each binary.

```
apps/server/
  cmd/
    mako-controlplane/main.go
    mako-worker/main.go
  internal/
    controlplane/   (see package map below)
    worker/         api/ config/ host/ runtime/ store/ tests/ transport/
```

## Package map (`internal/controlplane/`)

| Package | Status |
|---|---|
| `auth/` | **Implemented.** Password hashing, bearer tokens, bootstrap secret, user/credential/registration-invite lifecycle. See `security-model.md`. |
| `apiserver/middleware/` | **Implemented.** Shared bearer-auth (`RequireAuth`) and scope-auth (`RequireScope`) HTTP middleware. |
| `apiserver/commonauth/` | **Implemented.** Login/me/logout handlers shared by both admin and client surfaces — see `decisions-log.md` for why this package exists (it isn't in the original design spec). |
| `apiserver/adminapi/` | **Implemented.** Auth routes (setup, status, registration-invites), cross-owner instance list/start/stop/delete, user management (list/promote/demote/ban/unban/credential-revoke), audit-log retrieval, and guest-attachment revoke. |
| `apiserver/clientapi/` | **Partial.** Auth routes (register, credential revoke, retired pairing-invite tombstones), owner-scoped instance CRUD/lifecycle, and session/invite/signaling routes (create/get/resume/refresh/disconnect/promote/demote, PIN-based invite create+redeem, WebRTC offer/ICE relay) — served over HTTPS (`tlsidentity`) as of Phase 5. Image/pairing routes not built. |
| `instance/` | **Implemented.** Ownership-checked instance CRUD and start/stop/delete lifecycle, each against its own spawned-on-demand worker process (via `PlacementSelector`), plus `ListAllInstances`/`ListRunningInstanceIDs` for admin listing and the worker-liveness resync. `WorkerClientFor` resolves a `WorkerClient` from an instance's persisted worker base URL — the one place `transport` also goes through, instead of holding its own client. See `api-surfaces.md`. |
| `audit/` | **Implemented.** Shared `audit.Context`/`audit.Record` write helper plus cursor-paginated `audit.List` retrieval, used by `instance`, `session`, and `admin`. |
| `placement/` | **Implemented.** `Selector.Validate` still only accepts `"local-only"` (no worker pool to select among — every instance's worker is spawned fresh, 1:1), and now owns `AcquireWorker`/`ReleaseWorker`: allocate a port, spawn via `workerproc`, wait healthy, or kill by PID. See `decisions-log.md` (2026-10-14) and `docs/plans/worker-lifecycle.md`. |
| `workerproc/` | **Implemented.** OS-process mechanics underneath `placement`: a fake-able `Execer` (`OSExecer` wraps `os/exec`; `FakeExecer` is a reusable test double), `Spawn`, `WaitHealthy` (polls `/healthz`), `AllocateLocalPort`, `Kill`/`FromPID`. |
| `persistence/` | **Implemented.** Migration runner + sqlc-generated repo layer, migrations through `0012`. See `data-model.md`. |
| `controller/` | **Implemented.** `Reconciler` interface and workqueue-based dispatch run for real; `sessionProbe` is `reconcile.OwnerAbsenceProbe`, `workerProbe` is `reconcile.WorkerLivenessProbe` — both real. |
| `reconcile/` | **Implemented.** `OwnerAbsenceProbe` closes sessions past their owner-absence deadline and drives worker-side transport teardown. `WorkerLivenessProbe` health-checks each running instance's worker and respawns+adopts (via the worker's `/adopt` route) on failure. |
| `session/` | **Implemented.** Session/attachment CRUD lifecycle, session invites (6-digit PIN redemption, see `decisions-log.md`), and `KeyedMutex` request-time serialization. Routed on `clientapi`. See `docs/plans/session-transport-redesign.md`. |
| `transport/` | **Implemented.** Worker transport-session establish/teardown, signaling relay, and `TransitionAttachmentRole` (the promote/demote redesign — one method owns persist+worker-sync+retry, replacing four-times-patched route-layer orchestration). Routed on `clientapi`. |
| `admin/` | **Implemented.** Cross-owner user/credential/instance/audit management and ban/guest-revoke, backing `apiserver/adminapi`. See `docs/plans/admin-reconciliation.md`. |
| `tlsidentity/` | **Implemented, scoped.** Self-signed ECDSA P-256 cert/key generation (`Identity.GetOrCreate`), persisted and reused across runs. HTTPS trust bootstrap only — no PIN-pairing/SPKI pinning yet, see `pairing/` and `security-model.md`. |
| `image/`, `pairing/` | **Stub.** Each is an empty package with only a `doc.go` describing its intended responsibility. No implementation yet — `pairing/` deliberately deferred until a native client exists, see `docs/plans/tls-identity.md`. |

## Networking (current, not target)

(`apps/server/cmd/mako-controlplane/main.go`):

- Client API: `127.0.0.1:3000`, **HTTPS** with a self-signed certificate (`tlsidentity`). No client-side trust verification exists yet (no PIN-pairing/TOFU) — still loopback-only for that reason.
- Admin API: `127.0.0.1:3001`, plain HTTP — its loopback binding is the security boundary here, independent of transport encryption.

This does **not** yet match the longer-term target (client API LAN-reachable via PIN-based pairing) — that target is designed but unbuilt. See `security-model.md` for the gap in detail.
