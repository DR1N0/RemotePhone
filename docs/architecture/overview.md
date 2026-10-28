# System overview

> **Project status: paused indefinitely (v0.0.1).** The target games (Zenless Zone Zero / Genshin Impact) abort in the Android emulator's ARM translation layer after their anti-cheat loads. See the [README](../../README.md) and the anti-cheat entry in the decisions log.

This document describes the remotephone control plane **as it actually exists right now**. Unlike design/planning documents, this file is edited in place as reality changes — if it disagrees with the code, the code wins and this file is wrong.

## Motivation (why this exists instead of Moonlight/Sunshine/Apollo/Artemis)

The project started from two structural limits hit while using Artemis+Apollo (a Moonlight/Sunshine-family streaming pair) to play touch-native mobile games on a phone over a streamed PC session:

1. **No real touch semantics.** Desktop OSes have no native touchscreen concept; Apollo can only translate touch gestures into a virtual gamepad/mouse, which doesn't match games actually designed for touch input.
2. **Single input/output, no real multi-instance concurrency.** A physical host's keyboard/mouse/display are exclusive resources — two clients can never each independently drive two separate "instances" on the same host. That's the Windows/macOS multi-seat-input problem, not a gap in Apollo's implementation.

An Android emulator is touch-first by construction, and running several independent instances on one host is already an ordinary, solved capability (Android Studio runs multiple AVDs in parallel routinely). Wrapping each instance's full audio/video/touch I/O in WebRTC sidesteps the multi-seat problem entirely, rather than patching around it at the desktop-streaming layer. This is why remotephone is scoped to Android emulator instances specifically, not general desktop/game streaming — see `decisions-log.md` (2026-10-15) for the full discussion, including why this means remotephone isn't actually competing with desktop-streaming tools or general remote-desktop/cloud-gaming products (e.g. 网易UU远程) on "best streaming experience" — those solve a different problem than the one remotephone exists for.

## Status

- **`apps/server` (Go) is the active implementation.** One Go module (`module remotephone`, Go 1.27.2), two binaries: `cmd/remotephone-controlplane` and `cmd/remotephone-worker`. Auth, instance CRUD/lifecycle, session/transport (including signaling and PIN-based invites), admin/reconciliation (user management, audit-log retrieval, cross-owner instance listing, an owner-absence reconciler), worker lifecycle (real `exec.Command` spawning, health-checking, crash respawn with emulator adoption), the client surface's self-signed TLS identity, and base-image capture/promote (`image/`) are fully ported and routed. **Windows is supported as a host**: the server builds/tests natively, emulator/`qemu-img`/worker executable resolution is platform-aware, process liveness/stop use Win32 APIs (`OpenProcess`/`taskkill`) with an adb-first graceful emulator shutdown, and AMD `h264_amf` hardware encoding is probed at the real capture size. **A user may own multiple instances** (migration `0014` removed the one-per-owner unique index): each is created from a discovered template, started independently, and connected to at any time, and `GET /api/v1/templates` feeds the Android client's template picker. **Audio and multi-touch are implemented**: emulator `streamAudio` → 8 kHz mono PCMU over a WebRTC audio track, and emulator `sendTouch` with per-finger pointer IDs (adb remains the single-touch/keyboard fallback). WebRTC is IPv4/UDP-only and advertises every routable IPv4 interface (LAN, Tailscale, other VPNs), letting ICE choose — no Tailscale-specific config. Pairing is implemented as single-use access codes (self codes for adding a device; admin/owner unbound codes that can create an ordinary user or pair an existing account), with certificate pinning stored only in the saved profile keyed by stable server ID. The loopback owner console on 3001 can mint codes and remove accounts without a bearer token; it is guarded against browser CSRF/DNS rebinding. See `security-model.md` and `decisions-log.md` (2026-10-26). Every instance also gets its own independently-registered, cloned AVD for user-data isolation (`internal/worker/runtime/androidstudio`'s `ensureInstanceAVD`) — see `decisions-log.md` (2026-10-19/20) for why the emulator's own `-datadir`/`-init-data` flags don't achieve this and what does.
- **`apps/server/adminui`** contains the React owner console embedded into `remotephone-controlplane` via `go:embed` and served at `http://127.0.0.1:3001/admin/` by default (`REMOTEPHONE_ADMIN_PORT` selects the port; the bind host stays loopback). It is owner-only, loopback-only, and uses no login. `apps/controlplane` (TypeScript) was removed on 2026-10-26.
- **There is no separate `apps/worker` directory.** The old standalone Go worker was folded into `apps/server` as `internal/worker/` (subpackages: `api`, `config`, `host`, `runtime`, `store`, `tests`, `transport`), compiled into the `cmd/remotephone-worker` binary.
- **There is no browser-based streaming client, and none is planned.** Early design iterations (mid-2026) built a React/antd admin UI with a live `<video>` session page talking WebRTC directly to the worker. That page was progressively demoted to an internal diagnostic and is not part of the product surface. The only thing that ever establishes a streaming session against a worker is the Android client in [`apps/client`](../../apps/client), over a trusted LAN. Don't resurrect browser-streaming assumptions when reading old commit history or TS code — treat any TS code implementing a browser `<video>` session page as dead weight, not a spec to port.

## Process topology

Two OS processes, same host, same Go module:

- **`remotephone-controlplane`** — the orchestrator: auth, sessions, instances, admin API, client API, placement, and worker lifecycle management.
- **`remotephone-worker`** — a standalone process. The control plane spawns one ephemeral worker process **per running instance** (not a shared pool) via `internal/controlplane/workerproc` (the `exec.Command` mechanics) and `internal/controlplane/placement` (port allocation + when to spawn/release) — see `docs/architecture/decisions-log.md` (2026-10-14) and `planned-subsystems.md` for why. There is no single static `REMOTEPHONE_WORKER_BASE_URL` anymore; every worker interaction resolves a client from the specific instance's persisted `runtime_handle_json` (`instance.Service.WorkerClientFor`). `internal/controlplane/controller/controller.go`'s workqueue-based `Controller` runs both probes for real: `reconcile.OwnerAbsenceProbe` (session probe) and `reconcile.WorkerLivenessProbe` (worker probe) — the latter health-checks each running instance's worker and, on failure, kills the old process, spawns a replacement, and has it adopt the still-running emulator via the worker's `POST /v1/instances/{id}/adopt` route (no reboot). Both probes are periodically resynced from `cmd/remotephone-controlplane/main.go`. The worker is kept as a separate OS process (not an in-process goroutine) specifically so a control-plane restart doesn't kill active WebRTC sessions, and so a worker crash doesn't kill the emulator it launched (plain `exec.Command`, no process-group/`Pdeathsig` wiring — children are reparented to init, not killed, when their parent dies).

Both binaries share one module so `internal/` packages are trivially shared with one `go.sum`. Folder convention substitutes for a module boundary: `internal/contracts/` (not yet created) is meant to be the only package both binaries import; `internal/controlplane/` and `internal/worker/` hold everything specific to each binary.

```
apps/server/
  cmd/
    remotephone-controlplane/main.go
    remotephone-worker/main.go
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
| `apiserver/clientapi/` | **Implemented.** Auth routes (register, credential revoke, retired pairing-invite tombstones), owner-scoped instance CRUD/lifecycle, `GET /api/v1/templates` (host AVD discovery for the client picker), session/invite/signaling routes (create/get/resume/refresh/disconnect/promote/demote, PIN-based invite create+redeem, WebRTC offer/ICE relay), and base-image candidate capture — served over HTTPS (`tlsidentity`). Pairing routes not built. |
| `instance/` | **Implemented, multi-instance.** Ownership-checked instance CRUD and start/stop/delete lifecycle for any number of instances per owner, each against its own spawned-on-demand worker process (via `PlacementSelector`), plus `ListAllInstances`/`ListRunningInstanceIDs` for admin listing and the worker-liveness resync, and `ListTemplates` for client template discovery. `WorkerClientFor` resolves a `WorkerClient` from an instance's persisted worker base URL — the one place `transport` also goes through, instead of holding its own client. See `api-surfaces.md`. |
| `audit/` | **Implemented.** Shared `audit.Context`/`audit.Record` write helper plus cursor-paginated `audit.List` retrieval, used by `instance`, `session`, and `admin`. |
| `placement/` | **Implemented.** `Selector.Validate` still only accepts `"local-only"` (no worker pool to select among — every instance's worker is spawned fresh, 1:1), and now owns `AcquireWorker`/`ReleaseWorker`: allocate a port, spawn via `workerproc`, wait healthy, or kill by PID. See `decisions-log.md` (2026-10-14) and `docs/plans/worker-lifecycle.md`. |
| `workerproc/` | **Implemented.** OS-process mechanics underneath `placement`: a fake-able `Execer` (`OSExecer` wraps `os/exec`; `FakeExecer` is a reusable test double), `Spawn`, `WaitHealthy` (polls `/healthz`), `AllocateLocalPort`, `Kill`/`FromPID`. |
| `persistence/` | **Implemented.** Migration runner + sqlc-generated repo layer, migrations through `0012`. See `data-model.md`. |
| `controller/` | **Implemented.** `Reconciler` interface and workqueue-based dispatch run for real; `sessionProbe` is `reconcile.OwnerAbsenceProbe`, `workerProbe` is `reconcile.WorkerLivenessProbe` — both real. |
| `reconcile/` | **Implemented.** `OwnerAbsenceProbe` closes sessions past their owner-absence deadline and drives worker-side transport teardown. `WorkerLivenessProbe` health-checks each running instance's worker and respawns+adopts (via the worker's `/adopt` route) on failure. |
| `session/` | **Implemented.** Session/attachment CRUD lifecycle, session invites (6-digit PIN redemption, see `decisions-log.md`), and `KeyedMutex` request-time serialization. Routed on `clientapi`. See `docs/plans/session-transport-redesign.md`. |
| `transport/` | **Implemented.** Worker transport-session establish/teardown, signaling relay, and `TransitionAttachmentRole` (the promote/demote redesign — one method owns persist+worker-sync+retry, replacing four-times-patched route-layer orchestration). Routed on `clientapi`. |
| `admin/` | **Implemented.** Cross-owner user/credential/instance/audit management and ban/guest-revoke, backing `apiserver/adminapi`. See `docs/plans/admin-reconciliation.md`. |
| `tlsidentity/` | **Implemented, scoped.** Self-signed ECDSA P-256 cert/key generation (`Identity.GetOrCreate`), persisted and reused across runs. HTTPS trust bootstrap only; the client pins the certificate after access-code redemption/login. |
| `image/` | **Implemented, including worker-side QCOW2 layers.** Template/base-image version registry and capture/promote orchestration — `CaptureCandidateImage` (owner-scoped, inert until promoted), `PromoteBaseImage` (admin-only/owner-console `_SYSTEM`, moves a template's "current" pointer), `ResolveCurrentBaseImage` (consulted by `instance.Service` at start time). Worker capture flattens the full userdata chain and encryption-key overlay; instance creation makes a thin overlay and copies the key. Routed on both `clientapi` and `adminapi`. See `data-model.md` and `decisions-log.md` (2026-10-27). |
| `pairing/` | **Partially retained.** Supplies the startup bootstrap access-code value and certificate SHA-256; the old `/pairing/verify` endpoint is gone. The unified `auth` access-code store handles self/admin codes. |

## Networking (current, not target)

(`apps/server/cmd/remotephone-controlplane/main.go`):

- Client API: `127.0.0.1:3000` by default, **HTTPS** with a self-signed certificate (`tlsidentity`). Set `REMOTEPHONE_CLIENT_ADDR=0.0.0.0:3000` to reach it from a phone on the LAN; `REMOTEPHONE_CLIENT_PORT` can override just the port. The client trusts it only after a single-use access code and then pins the certificate; known servers skip the code.
- Owner console: `127.0.0.1:3001` by default (`REMOTEPHONE_ADMIN_PORT` changes the port; the host remains loopback), plain HTTP. Loopback is the owner boundary; `/api/v1/admin/*` accepts tokenless requests attributed to `_SYSTEM` (a bearer token is still accepted for programmatic clients). Same-origin UI requests are allowed; external Origins and non-JSON POSTs are rejected.
- Emulator graphics: `REMOTEPHONE_GPU_MODE` defaults to `host` on Windows and `auto` elsewhere. An explicit host-GPU test selected the AMD RX 9070 through Vulkan/GLES with WHPX enabled, even headless; this avoids silent SwiftShader fallback for games. Instances share the host GPU, VRAM, and H.264 encoder, so capacity is not disk-only. Windows service/Session 0 GPU behavior remains untested.

The client API is LAN-reachable and paired via access codes; see `security-model.md`.
