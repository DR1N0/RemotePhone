# Phase 2: Session & transport redesign

Living plan — edit in place as scope evolves. See `roadmap.md` for sequencing. This is the one phase in the roadmap flagged as a redesign, not a faithful port — see the dual-pending-role and promote/demote findings in `docs/architecture/decisions-log.md`'s originating research (summarized below with the actual fix now fully designed from reading the full TS source).

**Goal:** Sessions, attachments, role transitions, session invites, and WebRTC signaling relay, working end-to-end against the real worker and real pion transport.

**Depends on:** `instance` (Phase 1, done), `auth` (done).

## Schema change: migration `0012`

Collapse `attachments.restrictive_pending_role` into `attachments.pending_target_role`. Confirmed by reading `apps/controlplane/src/persistence/repositories/attachments.ts`'s `setAttachmentPendingTransportRole`: `restrictivePendingRole` is *always* written as `pendingTargetRole == null ? null : "observer"` — pure derived redundancy, every read site already ORs the two together to the same effect.

Also drop `attachments.invite_token` (added `0001`): confirmed zero references anywhere in the current TS codebase (`grep -rn "invite_token" apps/controlplane/src` — no hits outside unrelated `registrationInviteToken` naming) — a leftover from an old join-by-token flow superseded by the `session_invites` table + `/session-invites/redeem`. Same unused-column pattern as `workers`/`settings` (decisions-log, 2026-10-14).

```sql
ALTER TABLE attachments DROP COLUMN restrictive_pending_role;
ALTER TABLE attachments DROP COLUMN invite_token;
```

`effective_transport_role` becomes a pure function of `pending_target_role`: non-null → `observer`, else → `role`. No separate "restrictive" bit needed.

## The promote/demote redesign

TS's `routes/sessions.ts` promote and demote handlers hand-roll "persist → sync → on failure, mark pending + best-effort force observer" with **different orderings per direction** (promote: persist-then-sync; demote: sync-then-persist), rewritten four times across commits `c279217`→`8e25eac`→`e029c11`→`c4c2c74`. Worse: promote's ordering has a real race — the DB commits `role=participant` *before* the worker is told, so `effectiveTransportRole` briefly reports "participant" while the worker may still be enforcing "observer".

Replacement: one method, same shape for both directions. It lives on **`transport.Service`, not `session.Service`** — TS's own dependency direction is `transport-service.ts` → `session-service.ts` (transport imports session's types, never the reverse), so the orchestration combining a DB role mutation with a worker call belongs on the side that already depends on both, not the other way around (which would need `session` to import `transport`, inverting that direction and very likely creating an import cycle once `instance` is in the mix too). `session.Service` keeps two low-level DB-only primitives transport calls as plain steps: `SetAttachmentPendingTargetRole` and `ConfirmAttachmentRole`.

```go
func (t *Service) TransitionAttachmentRole(ctx context.Context, sessionID, attachmentID, targetRole string, actor audit.Context) (session.AttachmentRecord, error)
```

1. Authorize (caller owns the session; target attachment isn't the owner attachment); validate session is mutable (not closed).
2. **Mark pending first, always, before touching the worker:** one transaction sets `pending_target_role = targetRole` and records an `attempt` audit event. This immediately makes `effectiveTransportRole` report `observer` for anyone reading the row — closing promote's race, since nothing ever reports a role the worker hasn't been asked to enforce yet.
3. Call the worker (`UpdateTransportRole`) with the real `targetRole` — not `observer`, the actual target.
4. On success: one transaction sets `role = targetRole`, clears `pending_target_role`, records a `success` audit event.
5. On worker-call failure: best-effort call the worker again targeting `observer` explicitly (defensive — the failed update may have left it in an undefined state), swallow a second failure. Leave `pending_target_role` set (never cleared on this path) and record a `failure` audit event. Return the original error.
6. If step 4's transaction itself fails (worker succeeded, DB didn't): same `ErrAuditTerminalOutcomeUnavailable` escape hatch already built in `instance/errors.go` — reuse the pattern, don't reinvent it.

No "which order" question, no duplicated route-layer orchestration — `routes/sessions.ts`'s promote/demote handlers collapse to one call each.

## Session invite redesign: PIN instead of opaque token

TS's `session-invite-service.ts` issues a 43-character base64url token (same shape as a bearer credential) as the invite link's secret. Revisited during Phase 2 implementation (2026-10-14) in favor of something actually shareable out loud: a **6-digit numeric PIN**, valid for a **fixed 5 minutes** (not a caller-configurable TTL like the old 1–60 minute range) — a PIN's much smaller space (1,000,000 vs. 2^256) makes the validity window part of what keeps redemption safe, not just a UX knob.

Decisions made explicitly with the user, not assumed:
- **Lookup stays global** (`GetSessionInviteByTokenHash`, no instance/session scoping in the lookup key) — at this project's scale (one owner, a handful of concurrent sessions) a 1-in-a-million collision window between two simultaneously-active invites is a non-issue, and it keeps the link shortest: `host:port/join/<pin>`, no instance or session id in it at all.
- **Guest redemption keeps today's lightweight-user-row model** — redeeming still creates a real `users` row (`user_type = 'guest'`, display name only, no password), exactly as `session-invite-service.ts`'s `createGuestUser` does. A fully stateless alternative (attachment holds the display name directly, `user_id` stays NULL) was considered and explicitly declined — no schema change needed for this phase.
- PIN hashing reuses `auth.HashToken` (unsalted SHA-256) for consistency with every other credential in this schema, accepting that a 6-digit space is cheaply precomputable by anyone with DB read access — at which point far worse is already exposed. Not a peppered-hash scheme for this project's threat model.
- **No rate-limiting/attempt-cap was added in this pass.** A brute-force actor gets ~1M guesses against a 5-minute window; nothing else in this port has per-endpoint rate limiting either. Flagged here as a deliberately deferred hardening item, not an oversight — revisit before this ever leaves a trusted-LAN deployment model.

No schema change: `session_invites.token_hash` already stores an opaque hash with no format assumption baked into any query, so switching from a 256-bit token to a 6-digit PIN is a `session.Service`-layer change only (`CreateSessionInvite`/`RedeemSessionInvite`), not a migration.

## Correction to an earlier finding: the coordinator isn't fully replaced by the controller

The original research claimed `attachment-transport-coordinator.ts`'s per-key serialization is subsumed by the Go controller's workqueue. Re-reading the full source shows this conflates two different uses:

- **Background bulk teardown** (`reconciler.ts`, `admin-service.ts`'s `runMany` over many attachments during a sweep) — correctly replaced by the controller's single-item-at-a-time workqueue processing. No change to this part of the finding.
- **Request-time per-attachment serialization** (`session-service.ts`'s `disconnectAttachment`/`resumeAttachment`, `transport-service.ts`'s `ensureAttachmentTransport`/`syncAttachmentRole`/`destroyAttachmentTransport`/`teardownPendingAttachment`) — these run synchronously inside HTTP handlers, never touch the background controller at all. SQLite's single-writer connection pool already serializes the *DB* side, but the *worker HTTP calls* in between transactions are not covered by that — two concurrent requests for the same attachment (e.g. a disconnect racing a promote) could call the worker out of order. This needs its own in-process per-key lock.

Fix: a small `KeyedMutex` (package TBD, likely `session` itself) — `Lock(key string) func()`, backed by a `sync.Map` of lazily-created `*sync.Mutex`. Far simpler than porting the TS coordinator's promise-chaining `run`/`runMany` class; Go's blocking mutex semantics don't need that machinery. Used by `session`/`transport` methods that touch both DB and worker for the same attachment; not used by the controller's background probes.

## Scope

### `persistence`
- Migration `0012` (above).
- sqlc queries for `sessions`, `attachments`, `session_invites`: create, get, list-for-session/user, update role/status/pending-role/teardown-pending, close session, invite create/consume/get-by-hash.

### `session` package
Port `session-service.ts` and `session-invite-service.ts` into one package (they're tightly coupled — both operate on attachments and share `toAttachmentRecord`-equivalent mapping, which TS duplicates across the two files; Go gets one shared mapper):
- Session create (one active session per instance, owner attachment, attachment credential issuance) — transactional, matches `efd1606`'s atomicity fix already.
- `ConnectAttachment`, `DisconnectAttachment` (with owner-absence-deadline side effect), `ResumeAttachment` (credential rotation), `RefreshAttachmentCredential`.
- `SetAttachmentPendingTargetRole` / `ConfirmAttachmentRole` (new low-level primitives `transport.Service.TransitionAttachmentRole` orchestrates — see above) replacing `promoteAttachment`/`demoteAttachment`.
- `GetSession` (owner- or attachment-credential-scoped) / `GetSessionUnchecked` (for `transport` package).
- `CloseSession` (cascading attachment disconnect + credential revocation + teardown-pending marking).
- Session invites: create (owner-only, fixed 5-minute 6-digit PIN — see "Session invite redesign" above), redeem (registered or guest — guest creates a real `guest` user, no `enrollmentRequired`/`isServerOwner` fields, those columns don't exist in the Go schema).
- Guest display-name validation lives **once**, in the service — not duplicated in the route layer (finding #5 from the original research).

### `transport` package
Port `transport-service.ts` and `signaling-service.ts`:
- `EnsureAttachmentTransport`, `DestroyAttachmentTransport`, `TeardownPendingAttachment` — using the worker transport-session HTTP client methods (`CreateTransportSession`/`DestroyTransportSession`), extending `instance.WorkerClient` rather than inventing a parallel client.
- `RelayOffer`/`RelayICECandidate` — thin pass-through to the worker, as trivial as the TS version.
- `effectiveTransportRole` as a pure function taking `(role, pendingTargetRole string) string`.

### Routes
- `clientapi`: `/api/v1/sessions` (create/get), `/api/v1/sessions/:id/attachments/:attachmentId/{resume,credentials/refresh,disconnect,promote,demote}`, `/api/v1/sessions/:id/invites` (create a PIN), `/api/v1/session-invites/redeem` (redeem by PIN only — no session/instance id in the request path, see "Session invite redesign" above), `/api/v1/signaling/{session,ice-candidate}`. The retired `join`/`resume`-without-attachment-credential routes become `410 Gone` tombstones, same pattern as the auth phase's pairing-invite tombstones — not silently dropped.
- `adminapi`: none directly: session moderation in TS is owner-scoped only (`assertAccountOwnsSession`), no cross-owner session routes exist to port.

## Testing

Unit tests per package (fake `WorkerClient`, real SQLite temp-file DB, same pattern as `instance`). Specifically verify the promote/demote redesign: pending-role is set before any worker call; a failed worker call leaves pending set and never updates `role`; a failed persistence-after-success triggers `ErrAuditTerminalOutcomeUnavailable`. End-to-end smoke test against the real `remotephone-worker` binary for session create → attachment connect → signaling offer/ICE relay, reusing the real-worker harness from Phase 1's smoke test.

## Explicitly out of scope

- Admin cross-owner session operations — `admin` package, Phase 3.
- Owner-absence reconciliation wiring into the controller — Phase 3 (the `reconciler.ts` logic this phase's `DisconnectAttachment` sets up a deadline for).
