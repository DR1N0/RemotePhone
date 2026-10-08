import { mkdtempSync, rmSync } from "node:fs"
import { join } from "node:path"
import { tmpdir } from "node:os"

import { eq } from "drizzle-orm"
import { beforeEach, describe, expect, it } from "vitest"
import { createAppDb, createTestDb, resetTestDb } from "../src/persistence/db.js"
import { auditEvents, users, credentials, pairingInvites } from "../src/persistence/schema.js"
import {
  countActiveAdmins,
  createUser,
  getUserById,
  getUserByUsername,
  listUsers,
  toUserDto,
} from "../src/persistence/repositories/users.js"
import {
  consumeSessionInvite,
  createSessionInvite,
  getSessionInviteByTokenHash,
} from "../src/persistence/repositories/session-invites.js"
import { createAuditEvent } from "../src/persistence/repositories/audit-events.js"
import {
  createUser,
  getUserById,
  getServerOwnerUser,
  setUserStatus,
} from "../src/persistence/repositories/users.js"
import {
  createCredential,
  getCredentialByTokenHash,
  getCredentialById,
  revokeCredential,
} from "../src/persistence/repositories/credentials.js"
import {
  createPairingInvite,
  getPairingInviteByTokenHash,
  consumePairingInvite,
} from "../src/persistence/repositories/pairing-invites.js"
import {
  createAttachment,
  getAttachmentById,
  listAttachmentsForSession,
  setAttachmentPendingTransportRole,
  updateAttachmentRole,
  updateAttachmentStatus,
} from "../src/persistence/repositories/attachments.js"
import {
  createInstance,
  createInstanceIfOwnerAbsent,
  getInstanceById,
  listInstances,
  listInstancesByOwner,
  updateInstanceRuntime,
} from "../src/persistence/repositories/instances.js"
import {
  getSetting,
  putSetting,
} from "../src/persistence/repositories/settings.js"
import {
  clearOwnerAbsenceDeadline,
  closeSession,
  createSession,
  findOpenSessionForInstance,
  getSessionById,
  listSessionsForInstance,
  listSessionsWithExpiredOwnerAbsence,
  setOwnerAbsenceDeadline,
} from "../src/persistence/repositories/sessions.js"

function seedUser(db: ReturnType<typeof createTestDb>, id = "user_1"): void {
  if (db.select().from(users).where(eq(users.id, id)).get() != null) {
    return
  }
  createUser(db, {
    id,
    displayName: "Test Owner",
    isServerOwner: false,
    status: "active",
  })
}

function seedInstance(
  db: ReturnType<typeof createTestDb>,
  id = "inst_1",
  ownerId = "user_1",
): void {
  seedUser(db, ownerId)
  createInstance(db, {
    id,
    ownerId,
    name: "phone-1",
    runtimeClass: "android-studio-emulator",
    templateId: "macos-android-studio:Medium_Phone",
    placementPolicy: { kind: "local-only" },
    resourceProfile: {
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    },
    launchConfig: { headless: true, readOnly: true, wipeData: false },
    desiredState: "stopped",
    observedState: "stopped",
    runtimeHandle: null,
    stateDataRef: null,
    lastError: null,
  })
}

function listTableNames(db: ReturnType<typeof createTestDb>): string[] {
  const rows = db.$client
    .prepare("select name from sqlite_master where type = 'table' order by name")
    .all() as Array<{ name: string }>

  return rows.map((row) => row.name)
}

describe("persistence repositories", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("stores and lists managed instances with typed JSON-backed fields", () => {
    const db = createTestDb()

    seedInstance(db)

    updateInstanceRuntime(db, {
      id: "inst_1",
      observedState: "running",
      runtimeHandle: { pid: 4321, workerId: "local" },
      lastError: null,
    })

    const rows = listInstances(db)
    expect(rows).toHaveLength(1)
    expect(rows[0]?.name).toBe("phone-1")
    expect(rows[0]?.placementPolicy).toEqual({ kind: "local-only" })
    expect(rows[0]?.resourceProfile).toEqual({
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    })
    expect(rows[0]?.launchConfig).toEqual({ headless: true, readOnly: true, wipeData: false })
    expect(getInstanceById(db, "inst_1")?.observedState).toBe("running")
    expect(getInstanceById(db, "inst_1")?.runtimeHandle).toEqual({ pid: 4321, workerId: "local" })

    const stored = db.$client
      .prepare(`
        select
          placement_policy_json,
          resource_profile_json,
          launch_config_json,
          runtime_handle_json
        from instances
        where id = ?
      `)
      .get("inst_1") as {
      placement_policy_json: string
      resource_profile_json: string
      launch_config_json: string
      runtime_handle_json: string | null
    }

    expect(stored.placement_policy_json).toBe(JSON.stringify({ kind: "local-only" }))
    expect(stored.resource_profile_json).toBe(JSON.stringify({
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    }))
    expect(stored.launch_config_json).toBe(JSON.stringify({
      headless: true,
      readOnly: true,
      wipeData: false,
    }))
    expect(stored.runtime_handle_json).toBe(JSON.stringify({ pid: 4321, workerId: "local" }))
  })

  it("persists and lists instances scoped to their owner", () => {
    const db = createTestDb()
    seedInstance(db, "inst_a1", "owner_a")
    seedInstance(db, "inst_b1", "owner_b")

    const stored = getInstanceById(db, "inst_a1")
    expect(stored?.ownerId).toBe("owner_a")

    const ownedByA = listInstancesByOwner(db, "owner_a")
    expect(ownedByA.map((i) => i.id)).toEqual(["inst_a1"])
  })

  it("stores and updates sessions for an instance", () => {
    const db = createTestDb()

    seedInstance(db)

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: "reconnect-session-1",
    })

    setOwnerAbsenceDeadline(db, {
      id: "sess_1",
      ownerAbsenceDeadline: "2026-07-10T12:00:00.000Z",
    })

    let rows = listSessionsForInstance(db, "inst_1")
    expect(rows).toHaveLength(1)
    expect(getSessionById(db, "sess_1")?.status).toBe("active")
    expect(getSessionById(db, "sess_1")?.ownerAbsenceDeadline).toBe("2026-07-10T12:00:00.000Z")
    expect(findOpenSessionForInstance(db, "inst_1")?.id).toBe("sess_1")
    expect(listSessionsWithExpiredOwnerAbsence(db, "2026-07-10T12:00:01.000Z")).toEqual([
      expect.objectContaining({
        id: "sess_1",
        instanceId: "inst_1",
        status: "active",
      }),
    ])
    expect(listSessionsWithExpiredOwnerAbsence(db, "2026-07-10T11:59:59.000Z")).toEqual([])

    clearOwnerAbsenceDeadline(db, { id: "sess_1" })
    expect(getSessionById(db, "sess_1")?.status).toBe("active")
    expect(getSessionById(db, "sess_1")?.ownerAbsenceDeadline).toBeNull()

    closeSession(db, { id: "sess_1" })
    rows = listSessionsForInstance(db, "inst_1")
    expect(rows).toHaveLength(1)
    expect(getSessionById(db, "sess_1")?.status).toBe("closed")
    expect(findOpenSessionForInstance(db, "inst_1")).toBeUndefined()
  })

  it("stores attachments and supports explicit role and status updates", () => {
    const db = createTestDb()

    seedInstance(db)

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: "reconnect-session-1",
    })

    createAttachment(db, {
      id: "att_1",
      sessionId: "sess_1",
      clientId: "client_1",
      role: "observer",
      status: "connected",
      reconnectToken: "reconnect-attachment-1",
    })

    updateAttachmentRole(db, {
      id: "att_1",
      role: "participant",
    })
    updateAttachmentStatus(db, {
      id: "att_1",
      status: "disconnected",
    })
    setAttachmentPendingTransportRole(db, {
      id: "att_1",
      pendingTargetRole: "participant",
    })

    let rows = listAttachmentsForSession(db, "sess_1")
    expect(rows[0]).toMatchObject({
      restrictivePendingRole: "observer",
      pendingTargetRole: "participant",
    })

    setAttachmentPendingTransportRole(db, {
      id: "att_1",
      pendingTargetRole: "observer",
    })
    rows = listAttachmentsForSession(db, "sess_1")
    expect(rows).toHaveLength(1)
    expect(rows[0]?.role).toBe("participant")
    expect(rows[0]?.status).toBe("disconnected")
    expect(rows[0]?.restrictivePendingRole).toBe("observer")
    expect(rows[0]?.pendingTargetRole).toBe("observer")
    expect(getAttachmentById(db, "att_1")?.clientId).toBe("client_1")
  })

  it("upserts and reads server-level settings through typed JSON values", () => {
    const db = createTestDb()

    putSetting(db, {
      key: "session.idleTtlMs",
      value: { value: 300000 },
    })

    putSetting(db, {
      key: "session.idleTtlMs",
      value: { value: 600000 },
    })

    expect(getSetting<{ value: number }>(db, "session.idleTtlMs")?.value).toEqual({ value: 600000 })

    const stored = db.$client
      .prepare("select value_json from settings where key = ?")
      .get("session.idleTtlMs") as { value_json: string }

    expect(stored.value_json).toBe(JSON.stringify({ value: 600000 }))
  })

  it("creates the sqlite parent directory for a file-backed app database and applies migrations", () => {
    const testDb = createTestDb()
    expect(listTableNames(testDb)).toEqual(expect.arrayContaining([
      "__drizzle_migrations",
      "attachments",
      "audit_events",
      "instances",
      "session_invites",
      "sessions",
      "settings",
    ]))

    const root = mkdtempSync(join(tmpdir(), "mako-controlplane-db-"))
    const dbPath = join(root, "nested", "mako.db")

    let db = createAppDb(dbPath)
    expect(listTableNames(db)).toEqual(expect.arrayContaining([
      "__drizzle_migrations",
      "attachments",
      "audit_events",
      "instances",
      "session_invites",
      "sessions",
      "settings",
    ]))

    db.$client.close()
    rmSync(root, { recursive: true, force: true })
  })

  it("rejects sessions and attachments without their required parent records", () => {
    const db = createTestDb()

    expect(() =>
      createSession(db, {
        id: "sess_orphan",
        instanceId: "missing-instance",
        status: "active",
        ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
        reconnectToken: "reconnect-missing-session",
      }),
    ).toThrow()

    seedInstance(db)

    expect(() =>
      createAttachment(db, {
        id: "att_orphan",
        sessionId: "missing-session",
        clientId: "client-1",
        role: "observer",
        status: "connected",
        reconnectToken: "reconnect-missing-attachment",
      }),
    ).toThrow()
  })

  it("normalizes usernames on insert and lookup and returns safe user DTOs", () => {
    const db = createTestDb()

    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "  ALIce  ",
      passwordHash: "password-hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })

    const user = getUserByUsername(db, " Alice ")
    expect(user).toMatchObject({ id: "registered-user", username: "alice" })
    expect(getUserById(db, "registered-user")).toEqual(user)
    expect(toUserDto(user!)).toEqual({
      id: "registered-user",
      type: "registered",
      username: "alice",
      displayName: "Registered User",
      status: "active",
    })
    expect(() => createUser(db, {
      id: "duplicate-user",
      displayName: "Duplicate User",
      userType: "registered",
      username: "alice",
      passwordHash: "password-hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })).toThrow()

    createUser(db, {
      id: "active-admin",
      displayName: "Active Admin",
      userType: "admin",
      username: "Root",
      passwordHash: "password-hash",
      enrollmentRequired: false,
      isServerOwner: true,
      status: "active",
    })
    expect(listUsers(db).map((row) => row.id)).toEqual(expect.arrayContaining([
      "registered-user",
      "active-admin",
    ]))
    expect(countActiveAdmins(db)).toBe(1)
  })

  it("rejects a guest user with a login username", () => {
    const db = createTestDb()

    expect(() => createUser(db, {
      id: "guest-with-login",
      displayName: "Guest",
      userType: "guest",
      username: "guest-login",
      passwordHash: null,
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })).toThrow("guest users cannot have a login username")
    expect(getUserById(db, "guest-with-login")).toBeUndefined()
  })

  it("consumes a valid session invite exactly once", () => {
    const db = createTestDb()
    seedInstance(db)
    createSession(db, {
      id: "sess_invite",
      instanceId: "inst_1",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: "reconnect-session-invite",
    })
    createSessionInvite(db, {
      id: "session-invite-1",
      sessionId: "sess_invite",
      createdByUserId: "user_1",
      tokenHash: "session-invite-hash",
      expiresAt: "2026-10-03T00:00:00.000Z",
      consumedAt: null,
    })

    expect(consumeSessionInvite(db, {
      id: "session-invite-1",
      consumedAt: "2026-10-02T12:00:00.000Z",
      now: "2026-10-02T12:00:00.000Z",
    })).toBe(true)
    expect(consumeSessionInvite(db, {
      id: "session-invite-1",
      consumedAt: "2026-10-02T12:00:01.000Z",
      now: "2026-10-02T12:00:01.000Z",
    })).toBe(false)
    expect(getSessionInviteByTokenHash(db, "session-invite-hash")?.consumedAt).toBe(
      "2026-10-02T12:00:00.000Z",
    )
  })

  it("writes audit events through the repository", () => {
    const db = createTestDb()
    seedUser(db)

    createAuditEvent(db, {
      id: "audit-event-1",
      actorUserId: "user_1",
      actorCredentialId: null,
      action: "session.invite.issue",
      targetType: "session",
      targetId: "sess_1",
      requestId: "request-1",
      outcome: "success",
    })

    expect(db.select().from(auditEvents).get()).toMatchObject({
      id: "audit-event-1",
      actorUserId: "user_1",
      action: "session.invite.issue",
      outcome: "success",
    })
  })

  it("preserves duplicate instance owners for later conflict remediation", () => {
    const db = createTestDb()
    seedInstance(db, "inst_duplicate_a", "duplicate-owner")
    seedInstance(db, "inst_duplicate_b", "duplicate-owner")

    expect(listInstancesByOwner(db, "duplicate-owner").map((instance) => instance.id)).toEqual([
      "inst_duplicate_a",
      "inst_duplicate_b",
    ])
  })

  it("blocks another instance for a duplicated owner without deleting legacy rows", () => {
    const db = createTestDb()
    seedInstance(db, "inst_duplicate_a", "duplicate-owner")
    seedInstance(db, "inst_duplicate_b", "duplicate-owner")

    expect(createInstanceIfOwnerAbsent(db, {
      id: "inst_duplicate_c",
      ownerId: "duplicate-owner",
      name: "phone-3",
      runtimeClass: "android-studio-emulator",
      templateId: "macos-android-studio:Medium_Phone",
      placementPolicy: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      },
      launchConfig: { headless: true, readOnly: true, wipeData: false },
      desiredState: "stopped",
      observedState: "stopped",
      runtimeHandle: null,
      stateDataRef: null,
      lastError: null,
    })).toBe(false)
    expect(listInstancesByOwner(db, "duplicate-owner").map((instance) => instance.id)).toEqual([
      "inst_duplicate_a",
      "inst_duplicate_b",
    ])
  })
})

describe("users, credentials, pairing_invites schema", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("round-trips compatible user identities and account credentials", () => {
    const db = createTestDb()

    db.insert(users).values({
      id: "owner-1",
      displayName: "Server Owner",
      isServerOwner: true,
      userType: "admin",
      status: "active",
    }).run()
    db.insert(users).values({
      id: "guest-1",
      displayName: "Guest",
      isServerOwner: false,
      userType: "guest",
      status: "active",
    }).run()

    db.insert(credentials).values({
      id: "device-1",
      userId: "owner-1",
      tokenHash: "deadbeef",
      deviceLabel: "laptop-chrome",
      revokedAt: null,
    }).run()

    db.insert(pairingInvites).values({
      id: "invite_1",
      tokenHash: "cafef00d",
      createdBy: "owner-1",
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
      consumedAt: null,
      consumedByUserId: null,
    }).run()

    expect(db.select().from(users).where(eq(users.id, "owner-1")).get()).toMatchObject({
      id: "owner-1",
      userType: "admin",
      enrollmentRequired: true,
    })
    expect(db.select().from(users).where(eq(users.id, "guest-1")).get()).toMatchObject({
      id: "guest-1",
      userType: "guest",
      username: null,
    })
    expect(db.select().from(credentials).where(eq(credentials.id, "device-1")).get()).toMatchObject({
      id: "device-1",
      userId: "owner-1",
      scope: "account",
    })

    const inviteRow = db.select().from(pairingInvites).all()[0]
    expect(inviteRow).toMatchObject({
      id: "invite_1",
      tokenHash: "cafef00d",
      createdBy: "owner-1",
      consumedAt: null,
      consumedByUserId: null,
    })
  })

  it("rejects duplicate usernames regardless of case", () => {
    const db = createTestDb()

    db.insert(users).values({
      id: "registered-1",
      displayName: "Registered User",
      isServerOwner: false,
      userType: "registered",
      username: "Alice",
      status: "active",
    }).run()

    expect(() => db.insert(users).values({
      id: "registered-2",
      displayName: "Duplicate User",
      isServerOwner: false,
      userType: "registered",
      username: "alice",
      status: "active",
    }).run()).toThrow()
  })

  it("creates and looks up a server owner user and its credential", () => {
    const db = createTestDb()

    createUser(db, {
      id: "user_owner",
      displayName: "Server Owner",
      isServerOwner: true,
      status: "active",
    })

    createUser(db, {
      id: "user_other",
      displayName: "Someone Else",
      isServerOwner: false,
      status: "active",
    })

    expect(getUserById(db, "user_owner")?.displayName).toBe("Server Owner")
    expect(getServerOwnerUser(db)?.id).toBe("user_owner")

    setUserStatus(db, { id: "user_other", status: "banned" })
    expect(getUserById(db, "user_other")?.status).toBe("banned")

    createCredential(db, {
      id: "credential_owner",
      userId: "user_owner",
      tokenHash: "hash-owner",
      deviceLabel: "laptop-chrome",
      revokedAt: null,
    })

    expect(getCredentialByTokenHash(db, "hash-owner")?.id).toBe("credential_owner")
    expect(getCredentialById(db, "credential_owner")?.userId).toBe("user_owner")

    revokeCredential(db, { id: "credential_owner", revokedAt: "2026-09-21T00:00:00.000Z" })
    expect(getCredentialById(db, "credential_owner")?.revokedAt).toBe("2026-09-21T00:00:00.000Z")
  })

  it("creates, looks up, and consumes a pairing invite", () => {
    const db = createTestDb()

    createUser(db, {
      id: "user_owner",
      displayName: "Server Owner",
      isServerOwner: true,
      status: "active",
    })

    createPairingInvite(db, {
      id: "invite_2",
      tokenHash: "hash-invite-2",
      createdBy: "user_owner",
      expiresAt: "2026-09-22T00:00:00.000Z",
      consumedAt: null,
      consumedByUserId: null,
    })

    expect(getPairingInviteByTokenHash(db, "hash-invite-2")?.id).toBe("invite_2")

    createUser(db, {
      id: "user_new",
      displayName: "New Person",
      isServerOwner: false,
      status: "active",
    })

    expect(consumePairingInvite(db, {
      id: "invite_2",
      consumedAt: "2026-09-21T12:00:00.000Z",
      consumedByUserId: "user_new",
      now: "2026-09-21T12:00:00.000Z",
    })).toBe(true)

    const consumed = getPairingInviteByTokenHash(db, "hash-invite-2")
    expect(consumed?.consumedAt).toBe("2026-09-21T12:00:00.000Z")
    expect(consumed?.consumedByUserId).toBe("user_new")
  })
})
