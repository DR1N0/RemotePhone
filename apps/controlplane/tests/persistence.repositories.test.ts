import { mkdtempSync, rmSync } from "node:fs"
import { join } from "node:path"
import { tmpdir } from "node:os"

import { beforeEach, describe, expect, it } from "vitest"
import { createAppDb, createTestDb, resetTestDb } from "../src/persistence/db.js"
import { principals, credentials, pairingInvites } from "../src/persistence/schema.js"
import {
  createPrincipal,
  getPrincipalById,
  getServerOwnerPrincipal,
  setPrincipalStatus,
} from "../src/persistence/repositories/principals.js"
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
  getAttachmentByInviteToken,
  listAttachmentsForSession,
  setAttachmentPendingTransportRole,
  updateAttachmentRole,
  updateAttachmentStatus,
} from "../src/persistence/repositories/attachments.js"
import {
  createInstance,
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
  activateSession,
  closeSession,
  createSession,
  findOpenSessionForInstance,
  getSessionById,
  listIdleSessions,
  listSessionsForInstance,
  markSessionIdle,
} from "../src/persistence/repositories/sessions.js"

function seedPrincipal(db: ReturnType<typeof createTestDb>, id = "principal_1"): void {
  createPrincipal(db, {
    id,
    displayName: "Test Owner",
    isServerOwner: false,
    status: "active",
  })
}

function seedInstance(
  db: ReturnType<typeof createTestDb>,
  id = "inst_1",
  ownerId = "principal_1",
): void {
  seedPrincipal(db, ownerId)
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
      idleExpiresAt: null,
      reconnectToken: "reconnect-session-1",
    })

    markSessionIdle(db, {
      id: "sess_1",
      idleExpiresAt: "2026-07-10T12:00:00.000Z",
    })

    let rows = listSessionsForInstance(db, "inst_1")
    expect(rows).toHaveLength(1)
    expect(getSessionById(db, "sess_1")?.status).toBe("idle")
    expect(getSessionById(db, "sess_1")?.idleExpiresAt).toBe("2026-07-10T12:00:00.000Z")
    expect(findOpenSessionForInstance(db, "inst_1")?.id).toBe("sess_1")
    expect(listIdleSessions(db)).toEqual([
      expect.objectContaining({
        id: "sess_1",
        instanceId: "inst_1",
        status: "idle",
      }),
    ])

    activateSession(db, { id: "sess_1" })
    expect(getSessionById(db, "sess_1")?.status).toBe("active")
    expect(getSessionById(db, "sess_1")?.idleExpiresAt).toBeNull()

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
      idleExpiresAt: null,
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
      "instances",
      "sessions",
      "settings",
    ]))

    const root = mkdtempSync(join(tmpdir(), "mako-controlplane-db-"))
    const dbPath = join(root, "nested", "mako.db")

    let db = createAppDb(dbPath)
    expect(listTableNames(db)).toEqual(expect.arrayContaining([
      "__drizzle_migrations",
      "attachments",
      "instances",
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
        idleExpiresAt: null,
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

  it("stores and retrieves invite token", () => {
    const db = createTestDb()

    seedInstance(db)

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      idleExpiresAt: null,
      reconnectToken: "reconnect-session-1",
    })

    createAttachment(db, {
      id: "att-invite-1",
      sessionId: "sess_1",
      clientId: "client-1",
      role: "observer",
      status: "disconnected",
      reconnectToken: null,
      inviteToken: "tok-abc123",
    })

    const row = getAttachmentByInviteToken(db, "tok-abc123")
    expect(row).toBeDefined()
    expect(row?.id).toBe("att-invite-1")
  })
})

describe("principals, credentials, pairing_invites schema", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("round-trips a principal, a credential, and a pairing invite", () => {
    const db = createTestDb()

    db.insert(principals).values({
      id: "principal_1",
      displayName: "Server Owner",
      isServerOwner: true,
      status: "active",
    }).run()

    db.insert(credentials).values({
      id: "credential_1",
      principalId: "principal_1",
      tokenHash: "deadbeef",
      deviceLabel: "laptop-chrome",
      revokedAt: null,
    }).run()

    db.insert(pairingInvites).values({
      id: "invite_1",
      tokenHash: "cafef00d",
      createdBy: "principal_1",
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
      consumedAt: null,
      consumedByPrincipalId: null,
    }).run()

    const principalRow = db.select().from(principals).all()[0]
    expect(principalRow).toMatchObject({
      id: "principal_1",
      displayName: "Server Owner",
      isServerOwner: true,
      status: "active",
    })

    const credentialRow = db.select().from(credentials).all()[0]
    expect(credentialRow).toMatchObject({
      id: "credential_1",
      principalId: "principal_1",
      tokenHash: "deadbeef",
      deviceLabel: "laptop-chrome",
      revokedAt: null,
    })

    const inviteRow = db.select().from(pairingInvites).all()[0]
    expect(inviteRow).toMatchObject({
      id: "invite_1",
      tokenHash: "cafef00d",
      createdBy: "principal_1",
      consumedAt: null,
      consumedByPrincipalId: null,
    })
  })

  it("creates and looks up a server owner principal and its credential", () => {
    const db = createTestDb()

    createPrincipal(db, {
      id: "principal_owner",
      displayName: "Server Owner",
      isServerOwner: true,
      status: "active",
    })

    createPrincipal(db, {
      id: "principal_other",
      displayName: "Someone Else",
      isServerOwner: false,
      status: "active",
    })

    expect(getPrincipalById(db, "principal_owner")?.displayName).toBe("Server Owner")
    expect(getServerOwnerPrincipal(db)?.id).toBe("principal_owner")

    setPrincipalStatus(db, { id: "principal_other", status: "banned" })
    expect(getPrincipalById(db, "principal_other")?.status).toBe("banned")

    createCredential(db, {
      id: "credential_owner",
      principalId: "principal_owner",
      tokenHash: "hash-owner",
      deviceLabel: "laptop-chrome",
      revokedAt: null,
    })

    expect(getCredentialByTokenHash(db, "hash-owner")?.id).toBe("credential_owner")
    expect(getCredentialById(db, "credential_owner")?.principalId).toBe("principal_owner")

    revokeCredential(db, { id: "credential_owner", revokedAt: "2026-09-21T00:00:00.000Z" })
    expect(getCredentialById(db, "credential_owner")?.revokedAt).toBe("2026-09-21T00:00:00.000Z")
  })

  it("creates, looks up, and consumes a pairing invite", () => {
    const db = createTestDb()

    createPrincipal(db, {
      id: "principal_owner",
      displayName: "Server Owner",
      isServerOwner: true,
      status: "active",
    })

    createPairingInvite(db, {
      id: "invite_2",
      tokenHash: "hash-invite-2",
      createdBy: "principal_owner",
      expiresAt: "2026-09-22T00:00:00.000Z",
      consumedAt: null,
      consumedByPrincipalId: null,
    })

    expect(getPairingInviteByTokenHash(db, "hash-invite-2")?.id).toBe("invite_2")

    createPrincipal(db, {
      id: "principal_new",
      displayName: "New Person",
      isServerOwner: false,
      status: "active",
    })

    consumePairingInvite(db, {
      id: "invite_2",
      consumedAt: "2026-09-21T12:00:00.000Z",
      consumedByPrincipalId: "principal_new",
    })

    const consumed = getPairingInviteByTokenHash(db, "hash-invite-2")
    expect(consumed?.consumedAt).toBe("2026-09-21T12:00:00.000Z")
    expect(consumed?.consumedByPrincipalId).toBe("principal_new")
  })
})
