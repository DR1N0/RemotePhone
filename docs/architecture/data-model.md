# Data model

SQLite, migrations applied in order from `apps/server/internal/controlplane/persistence/migrations/` (currently `0000` through `0017`). This describes the schema **as migrated today**. It is not a copy of any design document — table/column names below were read directly from the migration files.

## Current tables

| Table | Purpose | Notable columns |
|---|---|---|
| `users` | Accounts (renamed from `principals` in `0008`). | `user_type` (`registered`/`admin`/`guest`/`system` — `system` is the hidden `_SYSTEM` owner actor added in `0016`, see below), `username` (unique, case-insensitive, may not start with `_`), `password_hash`, `status` (`active`/`banned`), `deleted_at` (soft-delete tombstone, `0017`). `is_server_owner` and `enrollment_required` were dropped in `0010` — see below. |
| `credentials` | Bearer-token-backed credentials, scoped to an account or a specific attachment. | `user_id`, `token_hash`, `scope` (`account`/`attachment`), `session_id`, `attachment_id`, `not_before`, `expires_at`, `revoked_at`. |
| `registration_invites` | Legacy single-use tokens an admin issues so a new user can self-register; the Android client now uses `access_codes`. Renamed from `pairing_invites` in `0010`. | `token_hash`, `created_by`, `expires_at`, `consumed_at`, `consumed_by_user_id`. |
| `access_codes` | Unified device-pairing / account-creation codes (`0015`). | `code_hash` (unique), nullable `target_user_id` (NULL = admin/unbound code that may create an ordinary user or pair an existing account; set = self code bound to that user), nullable `created_by_user_id`, `expires_at`, `used_at`, `failed_attempts` (invalidates after five wrong passwords). |
| `instances` | A managed runtime instance (an Android emulator session's durable record). | `runtime_class`, `template_id`, `placement_policy_json`, `resource_profile_json`, `launch_config_json`, `desired_state`/`observed_state`, `runtime_handle_json`, `state_data_ref`, `owner_id` (non-unique index as of `0014`; a user may own many instances). No `worker_id` column — the worker currently serving an instance is runtime status, tracked in `runtime_handle_json`/`observed_state`, not a separate column (see `decisions-log.md`, 2026-10-14). As of Phase 4 (worker lifecycle), `runtime_handle_json` (`runtime.InstanceHandle`) carries two more fields alongside the pre-existing emulator-process ones (`pid`/`consolePort`/`adbPort`/`grpcPort`): `workerPid` and `workerBaseUrl`, identifying the control-plane-spawned `remotephone-worker` OS process currently serving this instance — independent of the emulator fields, which survive a worker respawn unchanged. Still zero new schema: both live in the same JSON blob column. |
| `sessions` | A live session against an instance. | `instance_id`, `status`, `owner_absence_deadline` (renamed from `idle_expires_at` in `0006`), `owner_absence_timeout_ms`, `reconnect_token`. |
| `attachments` | One client's attachment to a session (a browser tab or future native client connection). | `session_id`, `client_id`, `role`, `status`, `user_id` (renamed from `principal_id`), `pending_target_role` (sole pending-role column as of `0012`, see below), `transport_teardown_pending`, `reconnect_token`. |
| `session_invites` | Single-use tokens inviting another user to attach to an existing session. | `session_id`, `created_by_user_id`, `token_hash`, `expires_at`, `consumed_at`. |
| `audit_events` | Append-only audit trail for admin/cross-owner actions. Written via `audit.Record`, read via `audit.List` (keyset-paginated by `(created_at, id)` DESC, cursor is an opaque event id — see `apiserver/adminapi`'s `GET /api/v1/admin/audit`). | `actor_user_id`, `actor_credential_id`, `action`, `target_type`, `target_id`, `request_id`, `outcome`. |
| `templates` | Roadmap Phase 6 (`0013`). Additive metadata keyed by the same `TemplateID` strings `androidstudio.DiscoverTemplates` already produces by scanning on-disk AVDs — not a replacement for discovery, and rows only exist for templates someone has captured a candidate image for. | `runtime_class`, `display_name`, `current_base_image_id` (nullable — null means "clone from the live, on-disk AVD," the behavior every template had before this table existed). |
| `base_images` | Roadmap Phase 6 (`0013`). An immutable, versioned snapshot of one instance's AVD state at capture time — inert until a `templates` row's `current_base_image_id` points at it. | `template_id`, `source_instance_id`, `artifact_path` (a directory holding a copied `config.ini`, a standalone flattened qcow2 `userdata-qemu.img`, and a raw `encryptionkey.img`; the userdata can serve as a backing file for many instances), `created_by_user_id`. |

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

`workers` and `settings` were dropped per the 2026-10-14 decisions-log entry (one-worker-per-instance model has no registry to populate; `settings` had zero callers). `instances_unique_owner` enforced the original one-instance-per-owner invariant; **migration `0014` removes it** — see "What `0014` did" below.

## What `0012` did

```sql
ALTER TABLE attachments DROP COLUMN restrictive_pending_role;
ALTER TABLE attachments DROP COLUMN invite_token;
```

`restrictive_pending_role` was pure derived redundancy: the pre-Go TypeScript implementation's `setAttachmentPendingTransportRole` always wrote it as `pendingTargetRole == null ? null : "observer"`, and every read site OR'd the two columns together to the same effect. `attachments.pending_target_role` is now the sole source for `effective_transport_role` (non-null → `observer`, else → `role`) — see `session.EffectiveTransportRole`. `invite_token` (added `0001`) had zero references anywhere in the TS codebase, superseded by the `session_invites` table — same unused-column pattern as `workers`/`settings`. See `docs/plans/session-transport-redesign.md`.

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

The original design (see `planned-subsystems.md`'s retired "Base image and user data model" section) assumed a base image would back a QEMU qcow2 backing-file chain, then was replaced by full copies because the emulator's `-datadir`/`-init-data` flags do not work with `-avd`. The current implementation restores the backing-chain model at the file level: `CaptureBaseImage` uses `qemu-img convert` to flatten the instance's complete userdata chain (including the emulator's `userdata-qemu.img.qcow2` top layer and any existing base overlay) into one standalone qcow2 artifact. It also flattens the emulator's `encryptionkey.img.qcow2` into a raw `encryptionkey.img`; without that key, a cloned instance cannot decrypt the captured userdata and hangs during boot. On instance creation, `ensureInstanceAVD` runs `qemu-img create -f qcow2 -b <base> -F qcow2 <instance>/userdata-qemu.img` and copies the base key, so each instance starts as a small writable overlay rather than a full copy. The emulator then adds its own `userdata-qemu.img.qcow2` child on top. `/sdcard` is part of the same `/data` filesystem on current AVDs (FUSE over `/data/media`), so external-storage resources are included in that userdata chain. See `decisions-log.md` (2026-10-27) for the live validation and remaining work (per-instance base pinning/GC, async capture, larger data partitions).

`templates` rows are created lazily, on first capture for a given `TemplateID` — never pre-populated from `androidstudio.DiscoverTemplates`'s on-disk AVD scan. A template with no row (the common case, for every template no admin has ever captured) behaves exactly as it did before this migration: new instances clone straight from the template's live, on-disk AVD.

## What `0014` did

```sql
DROP INDEX IF EXISTS instances_unique_owner;
CREATE INDEX IF NOT EXISTS instances_owner_id ON instances(owner_id);
```

The product model is now explicit multi-instance per user: one account may create any number of instances from any discovered template and start/connect them independently. The `0011` unique index was a leftover from the original one-seat-per-account MVP; it made a second `CreateManagedInstance` fail with `ErrOwnerConflict`. Migration `0014` drops that index and keeps a non-unique `(owner_id)` index for the owner-scoped listing query. `ErrOwnerConflict` and the 409 mapping were removed with it.

Two related changes shipped alongside: `GET /api/v1/templates` lets a client discover the host's real AVDs (instead of typing a template id), and `androidstudio.DiscoverTemplates` now skips `remotephone_instance_*` clones so one instance's per-instance AVD never appears as a selectable template.

## What `0015` did

```sql
CREATE TABLE access_codes (
  id TEXT PRIMARY KEY NOT NULL,
  code_hash TEXT NOT NULL UNIQUE,
  target_user_id TEXT REFERENCES users(id),
  created_by_user_id TEXT REFERENCES users(id),
  expires_at TEXT NOT NULL,
  used_at TEXT,
  failed_attempts INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);
```

A single code table replaces the two conceptual `pairing code` / `enrollment code` types. `target_user_id` NULL means an admin/owner-issued unbound code that may create a new ordinary user or pair an existing account; a set target means a self code bound to that account. Only hashes are stored; `failed_attempts` invalidates a code after five wrong-password attempts so a leaked unbound code cannot be used as an online password oracle.

## What `0016` did

```sql
INSERT OR IGNORE INTO users (... id='_SYSTEM', user_type='system', username=NULL, password_hash=NULL ...);
INSERT OR IGNORE INTO credentials (id='_SYSTEM', user_id='_SYSTEM', token_hash='_SYSTEM', ...);
```

Adds the hidden local-owner principal used as the audit actor for tokenless `/api/v1/admin/*` operations. It cannot authenticate; its credential exists only to satisfy `audit_events.actor_credential_id`. `CountRegisteredOrAdminUsers` and `ListAccountUsers` exclude `system`, so the seeded row does not make the server look claimed or appear in Users. Usernames must now start with a letter or digit; leading underscores are reserved.

## What `0017` did

```sql
ALTER TABLE users ADD COLUMN deleted_at TEXT;
```

Owner account removal is a soft delete: status becomes `banned`, username/password are cleared (releasing the name for a fresh account), device sessions/credentials are revoked, owned instances are stopped/deleted first, and the row remains for audit. `ListAccountUsers` filters `deleted_at IS NULL`; setup/admin counts intentionally still see the tombstone so deleting the last admin does not make the server look unclaimed.

