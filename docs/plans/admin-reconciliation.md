# Phase 3: Admin & reconciliation

**Status: Done.** Verified by unit/integration tests and a manual end-to-end run against the real `remotephone-controlplane` binary: admin setup, user list/promote/demote/ban/unban (including the last-active-admin guard), credential list/revoke, cross-owner instance list, audit-log retrieval (cursor pagination, newest-first), and the owner-absence reconciler (session created → owner disconnected → deadline expired → reconciler closed the session, revoked its credential, and attempted worker-side transport teardown — that teardown attempt fails in this phase's smoke test only because no real `remotephone-worker` is running, the same as it would for a live disconnect; no `remotephone-worker` run was required for this phase, unlike Phases 1/2).

Living plan — edit in place as scope evolves; not dated/immutable. See `roadmap.md` for how this fits the overall sequence.

**Goal:** a real `admin` package (user list/promote/demote/ban/unban/credential-revoke, cross-owner instance listing), audit-log retrieval with cursor pagination, and an owner-absence reconciler wired into the existing `controller.Controller`, exposed on `adminapi`. No migration needed — every column this phase reads/writes (`users.status`, `users.user_type`, `sessions.owner_absence_deadline`, `audit_events.*`) already exists.

**Depends on:** instance (Phase 1, done), session & transport (Phase 2, done).

## Scope

### `persistence`
New sqlc queries only, no schema change: `users` (`ListAccountUsers` cursor-paginated excluding guests, `UpdateUserType`, `SetUserStatus`, `CountActiveAdmins`), `credentials` (`ListAccountCredentialsForUser`, `GetAccountCredentialForUser`, both scope-filtered to `account`), `attachments` (`ListAttachmentsForUser`), `audit_events` (`ListAuditEvents` cursor-paginated via `sqlc.narg`, `GetAuditEventByID` for cursor validation), `sessions` (`ListSessionsWithExpiredOwnerAbsence`), `instances` (`ListInstances`, unscoped — distinct from the existing owner-scoped `ListInstancesByOwner`).

### `auth` package
Export two existing-but-private helpers instead of re-deriving the same checks in `admin`: `requireActiveAdmin` → `RequireActiveAdmin`, `toUserDTO` → `ToUserDTO`. Both already have the right shape (`toUserDTO`'s doc comment: "the ONE mapping function... never add a field here... never passwordHash" — admin's user listing must go through it, not a second copy).

### `session` package
Split `CloseSession`'s body into a private `closeSessionTx` core, reused by:
- `CloseSession` (existing signature, now a thin `withTx` wrapper — no behavior change, it has no callers yet)
- `CloseSessionTx(ctx, q *repo.Queries, sessionID string, actor audit.Context)` (new, exported) — lets the admin ban flow close a banned owner's sessions atomically with the ban itself, same pattern as `auth.requireActiveUserTx`.
- `CloseExpiredOwnerAbsenceSession(ctx, sessionID string, now time.Time, actor audit.Context) (record, attachmentIDs, closed bool, err error)` (new) — re-checks `status == "active" && owner_absence_deadline <= now` *inside* the same transaction it closes in, so it's naturally idempotent (a second call after the deadline was cleared by a resume, or after it already closed, is a no-op, not an error). This replaces TS's separate-select-then-recheck-in-transaction dance (`reconciler.ts`'s `beforeExpiredSessionTransaction` seam) — Go's model doesn't need the race guard TS needed.
- `ListExpiredOwnerAbsenceSessionIDs(ctx, now time.Time) ([]string, error)` (new) — candidate discovery for the reconciler's periodic resync only; `Reconcile` itself always re-verifies per-id, so this never needs to be race-free.

### `instance` package
Add `ListAllInstances(ctx) ([]ManagedInstance, error)` — cross-owner sibling of the existing owner-scoped `ListManagedInstances`, backed by the new `ListInstances` query. `ManagedInstance` is already the full admin-safe JSON shape (used directly by `adminapi/instances.go` today), so no separate DTO projection is needed the way TS's `toAdminInstanceDto` required.

### `audit` package
Add the read side the package's own doc comment already earmarked for this phase: an `Event`/`AuditDto`-equivalent type and `List(ctx, q *repo.Queries, limit int, cursor *string) (items, nextCursor *string, error)`, cursor-validated against `GetAuditEventByID` (invalid cursor → a new `ErrInvalidCursor`, mirroring TS's `InvalidAuditCursorError`). Keeps audit-log read ownership in the same package as the write (`Record`), matching how every other table's reads/writes stay in one package in this codebase.

### `admin` package (new, replaces the stub)
Port `admin-service.ts` as a `Service` depending on `auth.Service`, `session.Service`, `instance.Service` — deliberately **not** `transport.Service` (ban/guest-revoke do DB-only work in their own transaction and return affected `(sessionID, attachmentID)` pairs; the HTTP layer drives worker-side teardown afterward, mirroring `routes/admin.ts`'s split from `admin-service.ts`, and `clientapi/sessions.go`'s existing disconnect-then-teardown two-step). Methods: `RequireAdmin`, `ListUsers`, `ListCredentials`, `RevokeAccountCredential`, `ListInstances`, `ListAudit`, `SetUserType`, `SetBanStatus`, `RevokeGuestAttachment`. Reuses `auth.RequireActiveAdmin`/`ToUserDTO`, `session.CloseSessionTx`, `instance.ListAllInstances`, `audit.List`/`audit.Record`. New `AdminCredentialDto` (id/deviceLabel/scope/notBefore/expiresAt/revokedAt/createdAt/updatedAt — excludes tokenHash/userId/sessionId/attachmentId, mirroring TS). Last-active-admin guard via the new `CountActiveAdmins` query, same invariant TS enforces (`requireNotFinalActiveAdmin`).

### `controller` package
Add an exported `NoopReconciler{}` (returns `nil` unconditionally) to pass as `workerProbe` — Phase 4 owns the real worker-liveness probe (nothing to health-check until `exec.Command` spawning exists), but `controller.New` needs two non-nil `Reconciler`s and there's no reason to leave the already-tested `Controller` unwired for another phase.

### `reconcile` package (new)
`OwnerAbsenceProbe` wrapping `session.Service` + `transport.Service`, implementing `controller.Reconciler`: `Reconcile(ctx, sessionID)` calls `session.CloseExpiredOwnerAbsenceSession`, then loops the returned `attachmentIDs` through `transport.TeardownPendingAttachment` (same pattern as the admin HTTP layer, item above). This is the `sessionProbe` passed to `controller.New`.

### `adminapi` routes
New admin-only routes (requires active admin, service-layer-checked like `instance.Service` already does — not a separate HTTP middleware, matching this codebase's existing precedent that scope-level auth is middleware but admin-ness is a service check): `GET /api/v1/admin/users`, `GET /api/v1/admin/users/{id}/credentials`, `POST /api/v1/admin/users/{id}/credentials/{credentialId}/revoke`, `POST /api/v1/admin/users/{id}/type`, `POST /api/v1/admin/users/{id}/ban`, `POST /api/v1/admin/users/{id}/unban`, `POST /api/v1/admin/sessions/{id}/attachments/{attachmentId}/revoke`, `GET /api/v1/admin/audit`. Plus `GET /api/v1/admin/instances` (cross-owner list) on the existing `instances.go`, closing the TODO its own doc comment left.

### `main.go` wiring
Construct `admin.Service`, `reconcile.OwnerAbsenceProbe`, `controller.New(controller.NoopReconciler{}, ownerAbsenceProbe)`; run `ctrl.Run(ctx)` plus a resync ticker goroutine (`REMOTEPHONE_RECONCILE_INTERVAL_MS`, default 5s via the existing `envOrDefaultDuration` helper, matching TS's `reconcileIntervalMs` default) that calls `session.ListExpiredOwnerAbsenceSessionIDs` and `ctrl.EnqueueSession` per id. Register the new admin routes in `newAdminMux`.

## Explicitly out of scope for this phase

- Worker-liveness reconciliation (health-poll + respawn) — Phase 4; `controller.NoopReconciler` is the placeholder.
- Any schema/migration change — nothing new is needed.
- PIN brute-force rate-limiting, base-image/template admin routes, pairing/TLS — later phases, unrelated to this one's scope.
