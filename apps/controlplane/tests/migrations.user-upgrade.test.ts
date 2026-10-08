import { mkdtempSync, readFileSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join, resolve } from "node:path"

import Database from "better-sqlite3"
import { drizzle } from "drizzle-orm/better-sqlite3"
import { describe, expect, it } from "vitest"

import type { AppDb } from "../src/persistence/db.js"
import { inspectOwnerConflicts, ensureUniqueInstanceOwnerIndex, listInstancesByOwner } from "../src/persistence/repositories/instances.js"
import { appSchema } from "../src/persistence/schema.js"

const migrations = resolve(import.meta.dirname, "../src/persistence/migrations")

function apply(db: Database.Database, name: string): void {
  for (const statement of readFileSync(join(migrations, name), "utf8").split("--> statement-breakpoint")) {
    const sql = statement.trim()
    if (sql.length > 0) db.exec(sql)
  }
}

function createUpgradeFixture(): { db: AppDb; cleanup: () => void } {
  const directory = mkdtempSync(join(tmpdir(), "mako-user-upgrade-"))
  const path = join(directory, "fixture.db")
  const legacy = new Database(path)
  legacy.pragma("foreign_keys = ON")
  for (const migration of [
    "0000_plain_gwen_stacy.sql",
    "0001_invite_token.sql",
    "0002_restrictive_pending_role.sql",
    "0003_pending_target_role.sql",
    "0004_parallel_proteus.sql",
    "0005_instance_owner_and_attachment_principal.sql",
    "0006_owner_absence_timeout.sql",
    "0007_user_identity_foundation.sql",
  ]) apply(legacy, migration)

  legacy.prepare(`INSERT INTO principals (id, display_name, user_type, username, password_hash, enrollment_required, is_server_owner, status)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?)`)
    .run("user-1", "Administrator", "admin", "admin", "password-hash", 0, 1, "active")
  legacy.prepare(`INSERT INTO credentials (id, principal_id, token_hash, device_label, scope)
    VALUES (?, ?, ?, ?, ?)`)
    .run("credential-1", "user-1", "credential-hash", "laptop", "account")
  legacy.prepare(`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
    .run("inst-1", "user-1", "One", "android", "template", "{}", "{}", "{}", "running", "running")
  legacy.prepare(`INSERT INTO sessions (id, instance_id, status) VALUES (?, ?, ?)`)
    .run("session-1", "inst-1", "active")
  legacy.prepare(`INSERT INTO attachments (id, session_id, principal_id, client_id, role, status) VALUES (?, ?, ?, ?, ?, ?)`)
    .run("attachment-1", "session-1", "user-1", "client", "owner", "connected")
  legacy.prepare(`INSERT INTO pairing_invites (id, token_hash, created_by, expires_at, consumed_by_principal_id) VALUES (?, ?, ?, ?, ?)`)
    .run("invite-1", "invite-hash", "user-1", "2099-01-01T00:00:00.000Z", "user-1")
  legacy.prepare(`INSERT INTO session_invites (id, session_id, created_by_user_id, token_hash, expires_at) VALUES (?, ?, ?, ?, ?)`)
    .run("session-invite-1", "session-1", "user-1", "session-invite-hash", "2099-01-01T00:00:00.000Z")
  legacy.prepare(`INSERT INTO audit_events (id, actor_user_id, action, target_type, target_id, request_id, outcome) VALUES (?, ?, ?, ?, ?, ?, ?)`)
    .run("audit-1", "user-1", "test", "instance", "inst-1", "request-1", "success")
  apply(legacy, "0008_users_final.sql")
  expect(legacy.pragma("foreign_key_check")).toEqual([])
  const db = Object.assign(drizzle(legacy, { schema: appSchema }), { $client: legacy }) as AppDb

  return {
    db,
    cleanup: () => {
      legacy.close()
      rmSync(directory, { recursive: true, force: true })
    },
  }
}

describe("0008 user physical rename upgrade", () => {
  it("preserves user, credential, instance, session, attachment, admin and audit history", () => {
    const fixture = createUpgradeFixture()
    try {
      const rows = fixture.db.$client.prepare("SELECT id, username, user_type FROM users").all()
      expect(rows).toEqual([{ id: "user-1", username: "admin", user_type: "admin" }])
      expect(fixture.db.$client.prepare("SELECT user_id, token_hash FROM credentials").all()).toEqual([{ user_id: "user-1", token_hash: "credential-hash" }])
      expect(fixture.db.$client.prepare("SELECT owner_id FROM instances WHERE id = 'inst-1'").pluck().get()).toBe("user-1")
      expect(fixture.db.$client.prepare("SELECT instance_id FROM sessions WHERE id = 'session-1'").pluck().get()).toBe("inst-1")
      expect(fixture.db.$client.prepare("SELECT user_id FROM attachments WHERE id = 'attachment-1'").pluck().get()).toBe("user-1")
      expect(fixture.db.$client.prepare("SELECT consumed_by_user_id FROM pairing_invites WHERE id = 'invite-1'").pluck().get()).toBe("user-1")
      expect(fixture.db.$client.prepare("SELECT created_by_user_id FROM session_invites WHERE id = 'session-invite-1'").pluck().get()).toBe("user-1")
      expect(fixture.db.$client.prepare("SELECT actor_user_id FROM audit_events WHERE id = 'audit-1'").pluck().get()).toBe("user-1")
    } finally {
      fixture.cleanup()
    }
  })

  it("reports legacy duplicate and invalid owners without deleting them, then installs uniqueness after remediation", () => {
    const fixture = createUpgradeFixture()
    try {
      fixture.db.$client.prepare(`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`).run("inst-2", "user-1", "Two", "android", "template", "{}", "{}", "{}", "running", "running")
      expect(inspectOwnerConflicts(fixture.db)).toEqual([{ ownerId: "user-1", instanceIds: ["inst-1", "inst-2"], reason: "duplicate" }])
      expect(listInstancesByOwner(fixture.db, "user-1")).toHaveLength(2)
      expect(ensureUniqueInstanceOwnerIndex(fixture.db)).toBe(false)

      fixture.db.$client.prepare("DELETE FROM instances WHERE id = ?").run("inst-2")
      fixture.db.$client.pragma("foreign_keys = OFF")
      fixture.db.$client.prepare(`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
        .run("invalid-owner", "missing-user", "Invalid", "android", "template", "{}", "{}", "{}", "running", "running")
      fixture.db.$client.pragma("foreign_keys = ON")
      expect(inspectOwnerConflicts(fixture.db)).toEqual([{ ownerId: "missing-user", instanceIds: ["invalid-owner"], reason: "invalid-owner" }])
      expect(ensureUniqueInstanceOwnerIndex(fixture.db)).toBe(false)
      expect(fixture.db.$client.prepare("SELECT owner_id FROM instances WHERE id = 'invalid-owner'").pluck().get()).toBe("missing-user")

      fixture.db.$client.prepare("DELETE FROM instances WHERE id = ?").run("invalid-owner")
      expect(ensureUniqueInstanceOwnerIndex(fixture.db)).toBe(true)
      expect(() => fixture.db.$client.prepare(`INSERT INTO instances (id, owner_id, name, runtime_class, template_id, placement_policy_json, resource_profile_json, launch_config_json, desired_state, observed_state)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`).run("inst-3", "user-1", "Three", "android", "template", "{}", "{}", "{}", "running", "running")).toThrow(/UNIQUE constraint failed/)
    } finally {
      fixture.cleanup()
    }
  })
})
