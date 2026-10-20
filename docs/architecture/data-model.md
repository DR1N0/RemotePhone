# Data model

SQLite, migrations applied in order from `apps/server/internal/controlplane/persistence/migrations/` (currently `0000` through `0013`). This describes the schema **as migrated today**. It is not a copy of any design document — table/column names below were read directly from the migration files.

## Current tables

| Table | Purpose | Notable columns |
|---|---|---|
| `users` | Accounts (renamed from `principals` in `0008`). | `user_type` (`registered`/`admin`/`guest` — `guest` rows are created by session-invite redemption, see `decisions-log.md` 2026-10-14), `username` (unique, case-insensitive), `password_hash`, `status` (`active`/`banned`). `is_server_owner` and `enrollment_required` were dropped in `0010` — see below. |
| `credentials` | Bearer-token-backed credentials, scoped to an account or a specific attachment. | `user_id`, `token_hash`, `scope` (`account`/`attachment`), `session_id`, `attachment_id`, `not_before`, `expires_at`, `revoked_at`. |
| `registration_invites` | Single-use tokens an admin issues so a new user can self-register. Renamed from `pairing_invites` in `0010`. | `token_hash`, `created_by`, `expires_at`, `consumed_at`, `consumed_by_user_id`. |
| `instances` | A managed runtime instance (an Android emulator session's durable record). | `runtime_class`, `template_id`, `placement_policy_json`, `resource_profile_json`, `launch_config_json`, `desired_state`/`observed_state`, `runtime_handle_json`, `state_data_ref`, `owner_id` (unique-indexed as of `0011`, see below). No `worker_id` column — the worker currently serving an instance is runtime status, tracked in `runtime_handle_json`/`observed_state`, not a separate column (see `decisions-log.md`, 2026-10-14). As of Phase 4 (worker lifecycle), `runtime_handle_json` (`runtime.InstanceHandle`) carries two more fields alongside the pre-existing emulator-process ones (`pid`/`consolePort`/`adbPort`/`grpcPort`): `workerPid` and `workerBaseUrl`, identifying the control-plane-spawned `mako-worker` OS process currently serving this instance — independent of the emulator fields, which survive a worker respawn unchanged. Still zero new schema: both live in the same JSON blob column. |
| `sessions` | A live session against an instance. | `instance_id`, `status`, `owner_absence_deadline` (renamed from `idle_expires_at` in `0006`), `owner_absence_timeout_ms`, `reconnect_token`. |
| `attachments` | One client's attachment to a session (a browser tab or future native client connection). | `session_id`, `client_id`, `role`, `status`, `user_id` (renamed from `principal_id`), `pending_target_role` (sole pending-role column as of `0012`, see below), `transport_teardown_pending`, `reconnect_token`. |
| `session_invites` | Single-use tokens inviting another user to attach to an existing session. | `session_id`, `created_by_user_id`, `token_hash`, `expires_at`, `consumed_at`. |
| `audit_events` | Append-only audit trail for admin/cross-owner actions. Written via `audit.Record`, read via `audit.List` (keyset-paginated by `(created_at, id)` DESC, cursor is an opaque event id — see `apiserver/adminapi`'s `GET /api/v1/admin/audit`). | `actor_user_id`, `actor_credential_id`, `action`, `target_type`, `target_id`, `request_id`, `outcome`. |
| `templates` | Roadmap Phase 6 (`0013`). Additive metadata keyed by the same `TemplateID` strings `androidstudio.DiscoverTemplates` already produces by scanning on-disk AVDs — not a replacement for discovery, and rows only exist for templates someone has captured a candidate image for. | `runtime_class`, `display_name`, `current_base_image_id` (nullable — null means "clone from the live, on-disk AVD," the behavior every template had before this table existed). |
| `base_images` | Roadmap Phase 6 (`0013`). An immutable, versioned snapshot of one instance's AVD state at capture time — inert until a `templates` row's `current_base_image_id` points at it. | `template_id`, `source_instance_id`, `artifact_path` (a directory holding a copied `config.ini` + seed `userdata-qemu.img` — see "What `0013` did" below for why this isn't a qcow2 backing file), `created_by_user_id`. |

`workers` and `settings` existed briefly (added `0009`, never read or written by any code) and were dropped in `0011` — see "What `0011` did" below.

## What `0010` actually did, and why it isn't a bug

Migration `0010_drop_legacy_identity_columns.sql` runs three statements against the `users`/`registration_invites` tables:

```sql
ALTER TABLE users DROP COLUMN is_server_owner;
ALTER TABLE users DROP COLUMN enrollment_required;
ALTER TABLE pairing_invites RENAME TO registration_invites;
```

`is_server_owner` and `enrollment_required` were vestiges of an earlier, single-admin "Server-owner" identity model that was replaced by the current multi-admin `user_type` model; neither column was read by any surviving code path. `pairing_invites` was renamed because every other part of the domain calls this concept a "registration invite" (see the `registration_invites` table's own `CreateRegistrationInvite` API) — the old name was a leftover from the TypeScript codebase that nothing else in the Go port uses. See `decisions-log.md` for the full rationale.

## What `0011` did

Migration `0011_drop_workers_and_settings.sql` runs three statements:

```sql
DROP TABLE workers;
DROP TABLE settings;
CREATE UNIQUE INDEX instances_unique_owner ON instances(owner_id);
```

`workers` and `settings` were dropped per the 2026-10-14 decisions-log entry (one-worker-per-instance model has no registry to populate; `settings` had zero callers). `instances_unique_owner` enforces the one-instance-per-owner invariant directly in the database — `instance.Service.CreateManagedInstance` relies on this index and maps its constraint-violation error to `ErrOwnerConflict`, rather than porting the TS code's check-then-insert-inside-an-immediate-transaction dance (which existed only because the TS schema never had a real unique index — see `apps/controlplane/src/persistence/repositories/instances.ts`'s `ensureUniqueInstanceOwnerIndex`, a runtime remediation path for legacy pre-ownership data that doesn't exist in this fresh schema).

## What `0012` did

```sql
ALTER TABLE attachments DROP COLUMN restrictive_pending_role;
ALTER TABLE attachments DROP COLUMN invite_token;
```

`restrictive_pending_role` was pure derived redundancy: `apps/controlplane/src/persistence/repositories/attachments.ts`'s `setAttachmentPendingTransportRole` always wrote it as `pendingTargetRole == null ? null : "observer"`, and every read site OR'd the two columns together to the same effect. `attachments.pending_target_role` is now the sole source for `effective_transport_role` (non-null → `observer`, else → `role`) — see `session.EffectiveTransportRole`. `invite_token` (added `0001`) had zero references anywhere in the TS codebase, superseded by the `session_invites` table — same unused-column pattern as `workers`/`settings`. See `docs/plans/session-transport-redesign.md`.

## What `0013` did

```sql
CREATE TABLE templates (
  id TEXT PRIMARY KEY,
  runtime_class TEXT NOT NULL,
  display_name TEXT NOT NULL,
  current_base_image_id TEXT,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE base_images (
  id TEXT PRIMARY KEY,
  template_id TEXT NOT NULL REFERENCES templates(id),
  source_instance_id TEXT NOT NULL REFERENCES instances(id),
  artifact_path TEXT NOT NULL,
  created_by_user_id TEXT NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

Ships roadmap Phase 6 (`image.Service`): capturing a candidate image (owner-scoped, `clientapi`'s `POST /instances/:id/capture-candidate-image`) never affects anything live; promoting one (admin-only, `adminapi`'s `POST /admin/images/:candidateId/promote`) moves a `templates` row's `current_base_image_id`, the only step that changes what future instances of that template boot from. Rollback is the same primitive as promotion — promote an older candidate.

The original design (see `planned-subsystems.md`'s now-corrected "Base image and user data model" section) assumed a base image would back a QEMU qcow2 backing-file chain. Direct testing during the per-instance-isolation work that preceded this migration disproved that: the emulator's `-datadir`/`-init-data` flags, which that scheme depended on, turn out to be silently ignored (or rejected outright) once `-avd <name>` is given. The mechanism that actually works — and that `base_images.artifact_path` stores — is a plain directory holding a copied `config.ini` and seed `userdata-qemu.img`, the same two files `androidstudio.ensureInstanceAVD` already clones per-instance from. Capturing a candidate is just: copy those same two files out of a stopped instance's own AVD (`instances.state_data_ref`) into durable, versioned storage instead.

`templates` rows are created lazily, on first capture for a given `TemplateID` — never pre-populated from `androidstudio.DiscoverTemplates`'s on-disk AVD scan. A template with no row (the common case, for every template no admin has ever captured) behaves exactly as it did before this migration: new instances clone straight from the template's live, on-disk AVD.
