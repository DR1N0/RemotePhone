# Phase 1: Instance foundation

**Status: Done.** Verified both by unit/integration tests (fake worker client) and a manual end-to-end run against the real `mako-worker` binary — create → start (spawned and inspected a real Android Studio Emulator process) → stop (process exited) → delete (404 afterward). See "As built" below for where implementation diverged from this plan.

Living plan — edit in place as scope evolves; not dated/immutable. See `roadmap.md` for how this fits the overall sequence, and `docs/architecture/decisions-log.md` (2026-10-14) for why this phase has no `workers` table or placement-selection logic.

**Goal:** Instance CRUD/lifecycle works end-to-end against one configured worker, with an audit trail. No process spawning yet — matches today's TS behavior of pointing at one externally-started `mako-worker`.

**Depends on:** auth (done).

## Scope

### `audit` package
Port `audit-service.ts`'s `record()`/`list()` as a thin Go service over the existing `audit_events` table (`docs/architecture/data-model.md`). No findings against the TS version — straightforward port.

### `persistence`
Add sqlc queries for `instances`: create, get-by-id, list-by-owner, update-desired-state, update-observed-state. Follow the existing pipeline (`apps/server/internal/controlplane/persistence/queries/`, `make sqlc-generate`) already used for auth. No `workers` queries.

### `placement` package
Deliberately trivial for this phase: a function returning the one configured worker base URL (env/config-driven, matching `apps/controlplane/src/config/env.ts`'s `MAKO_WORKER_BASE_URL` pattern — e.g. `MAKO_WORKER_BASE_URL`, default `http://127.0.0.1:7780`). Kept as its own package (not inlined into `instance`) so Phase 4 can swap the internals for real `exec.Command` spawning without changing callers. No `Selector`-among-many interface — nothing to select among yet.

### Worker HTTP client
Port `worker-client.ts`'s HTTP client interface/types faithfully: start/stop/inspect instance. Collapse the TS version's ad hoc per-method error strings (`"Worker request failed with status …"`, `"Worker ICE relay failed with status …"`, etc.) into one typed error the caller can `errors.Is`-check, instead of distinct strings per call site.

### `instance` package
Port `instance-service.ts`'s CRUD/lifecycle:
- One-instance-per-owner creation, atomically: resolve the worker base URL (via `placement`) and create the instance row in the **same transaction** — fixing the TS version's TOCTOU gap where `createManagedInstance` validates placement before opening the transaction, then inserts separately (`instance-service.ts:240-268`). The resolved worker base URL lands in `runtime_handle_json`/`observed_state`, not a new column.
- Start/stop lifecycle: transactional persistence + audit-on-failure (`recordTerminalPersistenceFailure` in `instance-service.ts:140-157`) — don't silently lose a running-instance record if the DB write fails after the worker already started it.
- Keep the unchecked-accessor pattern (`getManagedInstanceUnchecked`, `instance-service.ts:280-282`) for future non-HTTP callers — Phase 2's `transport` package will need it.
- Owner vs. admin authorization split (`assertCanManageInstance` / `assertCanAdminManageInstance`) — port as-is, it's clean.

### Routes
`adminapi`/`clientapi` instance CRUD routes mirroring `routes/instances.ts`'s owner-scoping split (owner-scoped on `clientapi`, cross-owner on `adminapi`).

## Explicitly out of scope for this phase

- Spawning/supervising worker processes (`exec.Command`) — Phase 4.
- Any `workers` table or registry — not being built (see decisions-log).
- Session/attachment/transport — Phase 2.

## As built (deviations from the plan above)

- **`audit` shipped minimal, not a full `record()`/`list()` port**: just `audit.Context` + `audit.Record` (one function wrapping `repo.CreateAuditEvent`). Audit-log *retrieval* (`list()`) has no caller yet — nothing needs it until Phase 3's admin routes do, so it wasn't built (YAGNI). `auth/service.go` still writes audit events inline via `repo.CreateAuditEvent` directly, predating this package; it wasn't refactored to use it, to avoid churning already-shipped, tested code.
- **`placement` is `internal/controlplane/placement`, a top-level package — not nested inside `instance`.** The Phase 1 module skeleton already had an empty `placement/` package reserved for exactly this; an earlier draft of this implementation put the logic inside `instance/placement.go` instead, which was a mistake, caught and corrected before merging. `Placement`/`PlacementPolicy` are `placement.Selector`/`placement.Policy`.
- **No explicit transaction for "validate placement + insert" atomicity.** The plan above describes fixing instance-service.ts's TOCTOU by wrapping placement validation and the insert in one transaction. That framing assumed placement validation might read from a DB-backed `workers` table (a race window). Since placement validation ended up pure in-memory (`Selector.Validate`, no DB access — see decisions-log 2026-10-14), there's no race to close: a single `INSERT ... RETURNING` guarded by the `instances_unique_owner` DB index (migration `0011`) is already atomic on its own.
- **Migration `0011`** (drop `workers`/`settings`, add `instances_unique_owner`) landed as part of this phase's persistence work, since it was touching `instances` queries anyway.
