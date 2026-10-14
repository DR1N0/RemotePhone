# Decisions log

This project previously tracked design decisions as a trail of dated, immutable spec/plan documents (`docs/superpowers/specs/`, `docs/superpowers/plans/`). That methodology is retired and those folders have been deleted (2026-10-13) — this log, plus the other files in `docs/architecture/`, is what replaces them as the current-state source of truth.

**Why this log exists:** `docs/architecture/*.md` describe current reality and get edited in place. When a change deviates from what those files previously said, or introduces something non-obvious that isn't self-explanatory from the code, add an entry here — short, self-contained, no dangling references to documents that might not exist later — and update the relevant `architecture/*.md` file in the same change.

Entries are reverse-chronological.

---

### 2026-10-14 — Drop the `workers` and `settings` tables (decided; migration not yet written)

While scoping the instance/placement phase, re-examined whether a persisted `workers` registry (added `0009`, unused by any code) was actually needed. The original justification was a shared pool of long-lived local worker processes requiring health-based placement selection and restart affinity. Two things undermined that:

1. `internal/worker/runtime/registry.go`'s `handles map[string]InstanceHandle` shows a single `mako-worker` process already multiplexes multiple concurrent instances internally — "multiple local workers" was never a hard technical requirement, just a carried-over assumption.
2. The better MVP model is **one worker process spawned on demand per running instance** (`exec.Command`, torn down when the instance stops), not a shared pool. Under that model there is no placement *selection* problem (nothing to choose among — every instance start spawns its own fresh worker) and no *affinity* problem (an instance's durable state lives in its qcow2 overlay on disk, not in worker-process memory, so there's nothing to preserve across a respawn). It also strictly improves fault isolation versus any shared-worker arrangement: a worker crash can only ever affect the one instance it was spawned for.

Consequence: no `workers` table, no `instances.worker_id` column. Whichever process is currently serving an instance is runtime status, not durable config, and belongs in the already-existing `instances.observed_state`/`runtime_handle_json` fields (already a spec/status split) — zero new schema for worker tracking. A `workers` table would become justified again only if a pooled/remote worker legitimately serving multiple instances is introduced later; see `planned-subsystems.md`'s worker-lifecycle section for the current (revised) design target.

Separately, confirmed `settings` (generic key/value table) has zero callers anywhere in `apps/controlplane` — `persistence/repositories/settings.ts`'s `putSetting`/`getSetting` are dead code, same speculative-and-unused pattern as `workers`. Dropping both.

Neither drop has been executed as a migration yet as of this writing — `data-model.md` still lists both tables as physically present (truthful to the migrated schema) with a pointer back to this entry. Update both files together once the removal migration lands.

### 2026-10-13 — Go bumped to 1.27.2; superpowers methodology and artifacts retired

`apps/server/go.mod` moved from `go 1.26.0` to `go 1.27.2` to match the locally installed toolchain. This is a personal, pre-release, unreleased repository with no deployed users — there's no reason to lag behind the installed compiler, and the bump is a no-op risk-wise (verified: `go build ./...` and `go test ./...` both pass unchanged after the bump).

Simultaneously, the "superpowers" spec/plan-writing methodology was dropped from use, and `docs/superpowers/` (specs + plans) and the repo-root `.superpowers/` (subagent-driven-development scratch: progress files, review diffs) were deleted. Both directories were already `.gitignore`d and untracked, so deletion didn't touch git history. Everything from those documents still relevant to understanding the system today was extracted into this `docs/architecture/` folder before deletion — nothing was lost, but there is no longer a historical trail to cite; decisions are recorded here with full inline rationale instead of citations.

### Earlier (introduced during the auth port, prior to 2026-10-13)

**`apiserver/commonauth` package.** Login, `/auth/me`, and logout need to be registered identically on both the admin and client HTTP surfaces, but `adminapi` and `clientapi` are sibling packages that must not import each other (keeping the loopback/LAN network boundary meaningful depends on them staying independent). A third, small shared package (`commonauth`) holds those three handlers once, imported by both surfaces, instead of duplicating the handler code or collapsing the surfaces together.

**Migration `0010`: dropped `users.is_server_owner` and `users.enrollment_required`; renamed `pairing_invites` → `registration_invites`; dropped the `EnrollLegacy` method/route.** `is_server_owner` and `enrollment_required` were vestiges of an earlier single-admin "Server-owner" identity model, superseded by the current multi-admin `user_type` model — neither column was read by any surviving code path, and `EnrollLegacy` existed only to migrate old passwordless "Principal" accounts into that superseded model. Since this is a pre-release project with no such accounts to migrate, the whole bridge was deleted rather than ported. `pairing_invites` was renamed because the actual domain concept is called a "registration invite" everywhere else in the code (`CreateRegistrationInvite`, the `registration_invites` table itself) — `pairing_invites` was a naming leftover nothing else used. The client-facing `POST /api/v1/auth/pairing-invites` and `.../redeem` routes were kept as `410 Gone` tombstones rather than deleted outright, so a stale caller gets an explicit "retired" response instead of an ambiguous 404.

### 2026-10-10 — No browser-based streaming client, ever

Design iterations from mid-2026 built a real browser WebRTC client: a React/antd admin UI with a live `<video>` session page, SDP/ICE relay through the control plane, pion-based WebRTC in the worker. Over successive iterations that page was progressively demoted — first to "internal diagnostic, not a product entry point" (no further investment in its mDNS/ICE interoperability issues), then to "development-only, unavailable by default in production" — before being dropped entirely: the only client that will ever establish a streaming session against a worker is a future native `mako-client` app, connecting over a trusted LAN. There is no plan to revive a browser client. Treat any TS code implementing the old `Session.tsx` browser page as dead weight when porting, not as something to carry into the Go control plane.
