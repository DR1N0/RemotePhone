# System overview

This document describes the mako control plane **as it actually exists right now**. Unlike design/planning documents, this file is edited in place as reality changes — if it disagrees with the code, the code wins and this file is wrong.

## Status

- **`apps/server` (Go) is the active implementation.** One Go module (`module mako`, Go 1.27.2), two binaries: `cmd/mako-controlplane` and `cmd/mako-worker`. Auth and instance CRUD/lifecycle are fully ported and live; session, admin, image, pairing, and real worker-lifecycle management are still empty stub packages.
- **`apps/controlplane` (TypeScript) is legacy**, last touched 2026-10-08. It is the reference implementation the Go port is being built from, not the running system going forward. It has not been deleted because large parts of its behavior (instance/session/admin/audit services) haven't been ported yet and it's still read as a behavioral reference.
- **There is no separate `apps/worker` directory.** The old standalone Go worker was folded into `apps/server` as `internal/worker/` (subpackages: `api`, `config`, `host`, `runtime`, `store`, `tests`, `transport`), compiled into the `cmd/mako-worker` binary.
- **There is no browser-based streaming client, and none is planned.** Early design iterations (mid-2026) built a React/antd admin UI with a live `<video>` session page talking WebRTC directly to the worker. That page was progressively demoted to an internal diagnostic and is not part of the product surface. The only thing that ever establishes a streaming session against a worker is the future native `mako-client` app, over a trusted LAN. Don't resurrect browser-streaming assumptions when reading old commit history or TS code — treat any TS code implementing a browser `<video>` session page as dead weight, not a spec to port.

## Process topology

Two OS processes, same host, same Go module:

- **`mako-controlplane`** — the orchestrator: auth, sessions, instances, admin API, client API, placement, and (eventually) worker lifecycle management.
- **`mako-worker`** — a standalone process. Today exactly one worker is expected, started manually/externally and reachable at a configured base URL (`MAKO_WORKER_BASE_URL`). Once worker-lifecycle management is built, the control plane will spawn one ephemeral worker process **per running instance** (not a shared pool) — see `docs/architecture/decisions-log.md` (2026-10-14) and `planned-subsystems.md` for why. Today there is no supervision logic yet — `internal/controlplane/controller/controller.go` only has the dispatch skeleton from Phase 1, with no real worker-liveness or session reconcilers plugged in. The worker is kept as a separate OS process (not an in-process goroutine) specifically so a control-plane restart doesn't kill active WebRTC sessions.

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
| `apiserver/adminapi/` | **Partial.** Auth routes (setup, status, registration-invites) plus cross-owner instance start/stop/delete. No listing-all-instances route yet (belongs to `admin/`, not built). |
| `apiserver/clientapi/` | **Partial.** Auth routes (register, credential revoke, retired pairing-invite tombstones) plus owner-scoped instance CRUD/lifecycle (create/list/get/start/stop/delete). |
| `instance/` | **Implemented.** Ownership-checked instance CRUD and start/stop/delete lifecycle against one configured worker (`MAKO_WORKER_BASE_URL`, default `http://127.0.0.1:7780`). See `api-surfaces.md`. |
| `audit/` | **Implemented, minimal.** Shared `audit.Context`/`audit.Record` helper used by `instance` (and future `session`/`admin`). Audit-log *retrieval* isn't built — nothing needs it until `admin` does. |
| `placement/` | **Implemented, deliberately trivial.** `Selector.Validate` rejects any policy kind but `local-only` — there's no worker pool to select among yet. See `decisions-log.md` (2026-10-14). |
| `persistence/` | **Implemented.** Migration runner + sqlc-generated repo layer, migrations through `0012`. See `data-model.md`. |
| `controller/` | **Skeleton only.** `Reconciler` interface and workqueue-based dispatch exist; no real probes (worker-liveness, owner-absence) are registered yet. |
| `session/` | **Partial.** Session/attachment CRUD lifecycle, `KeyedMutex` request-time serialization, and the two low-level role-transition primitives (`SetAttachmentPendingTargetRole`/`ConfirmAttachmentRole`) are implemented. Session invites and HTTP routes are not wired yet. See `docs/plans/session-transport-redesign.md`. |
| `transport/`, `admin/`, `image/`, `pairing/`, `tlsidentity/` | **Stub.** Each is an empty package with only a `doc.go` describing its intended responsibility. No implementation yet. |

## Networking (current, not target)

Both HTTP surfaces currently run **plain HTTP on loopback only** (`apps/server/cmd/mako-controlplane/main.go`):

- Client API: `127.0.0.1:3000`
- Admin API: `127.0.0.1:3001`

This matches the pre-Go-port TypeScript deployment's port numbers. It does **not** yet match the longer-term target (client API reachable over the LAN via HTTPS with PIN-based pairing) — that target is designed but unbuilt. See `security-model.md` for the gap in detail.
