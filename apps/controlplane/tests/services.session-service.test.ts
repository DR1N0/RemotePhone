import { randomUUID } from "node:crypto"

import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment, getAttachmentById } from "../src/persistence/repositories/attachments.js"
import { listAuditEvents } from "../src/persistence/repositories/audit-events.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createAuthService, type AuthenticatedUser } from "../src/services/auth-service.js"
import {
  SessionForbiddenError,
  SessionInvalidRequestError,
  createSessionService,
} from "../src/services/session-service.js"

const now = () => new Date("2026-10-02T12:00:00.000Z")

function seedUser(db: ReturnType<typeof createTestDb>, id: string, type: "registered" | "admin" = "registered"): void {
  createUser(db, {
    id,
    displayName: id,
    userType: type,
    username: id,
    passwordHash: "hash",
    enrollmentRequired: false,
    isServerOwner: type === "admin",
    status: "active",
  })
}

function seedInstance(db: ReturnType<typeof createTestDb>, ownerId = "owner"): void {
  createInstance(db, {
    id: "instance",
    ownerId,
    name: "phone",
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
    desiredState: "running",
    observedState: "running",
    runtimeHandle: null,
    stateDataRef: null,
    lastError: null,
  })
}

async function accountCaller(authService: ReturnType<typeof createAuthService>, userId: string): Promise<AuthenticatedUser> {
  const token = authService.issueAccountCredential(userId, "test")
  const caller = await authService.authenticate(token)
  if (caller == null) throw new Error("expected account caller")
  return caller
}

function createBoundObserver(input: {
  db: ReturnType<typeof createTestDb>
  authService: ReturnType<typeof createAuthService>
  sessionId: string
  userId?: string
}): { attachmentId: string; token: string } {
  const userId = input.userId ?? "observer"
  seedUser(input.db, userId)
  const attachmentId = randomUUID()
  createAttachment(input.db, {
    id: attachmentId,
    sessionId: input.sessionId,
    userId: userId,
    clientId: `${userId}-client`,
    role: "observer",
    status: "connected",
  })
  return {
    attachmentId,
    token: input.authService.issueAttachmentCredential({
      userId,
      sessionId: input.sessionId,
      attachmentId,
      expiresAt: new Date(now().getTime() + 24 * 60 * 60_000).toISOString(),
    }),
  }
}

describe("session service authorization", () => {
  beforeEach(() => resetTestDb())

  it("issues a bound owner attachment credential once while creating a session", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })

    const created = await service.createSession({
      instanceId: "instance",
      ownerClientId: "owner-client",
      caller: await accountCaller(authService, "owner"),
    })

    expect(created.session.attachments).toEqual([
      expect.objectContaining({ clientId: "owner-client", userId: "owner", role: "owner" }),
    ])
    expect(created.session).not.toHaveProperty("reconnectToken")
    expect(created.session.attachments[0]).not.toHaveProperty("reconnectToken")
    const attachmentCredentialCount = db.$client
      .prepare("SELECT COUNT(*) AS count FROM credentials WHERE scope = 'attachment'")
      .get() as { count: number }
    expect(attachmentCredentialCount.count).toBe(1)
    expect(await authService.authenticate(created.attachmentToken)).toMatchObject({
      userId: "owner",
      scope: "attachment",
      sessionId: created.session.id,
      attachmentId: created.session.attachments[0]?.id,
    })
  })

  it("allows only the owning account or a session-bound attachment to read a session", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedUser(db, "stranger", "admin")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const attached = await authService.authenticate(created.attachmentToken)
    if (attached == null) throw new Error("expected attachment caller")

    await expect(service.getSession(created.session.id, owner)).resolves.toEqual(expect.objectContaining({ id: created.session.id }))
    await expect(service.getSession(created.session.id, attached)).resolves.toEqual(expect.objectContaining({ id: created.session.id }))
    await expect(service.getSession(created.session.id, await accountCaller(authService, "stranger")))
      .rejects.toBeInstanceOf(SessionForbiddenError)
  })

  it("requires a matching attachment credential to disconnect, resume, and refresh", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const attachment = created.session.attachments[0]
    const attached = await authService.authenticate(created.attachmentToken)
    if (attachment == null || attached == null) throw new Error("expected owner attachment")

    const disconnected = await service.disconnectAttachment({
      sessionId: created.session.id,
      attachmentId: attachment.id,
      caller: attached,
    })
    expect(disconnected.ownerAbsenceDeadline).toBe("2026-10-02T12:05:00.000Z")

    const resumed = await service.resumeAttachment({
      sessionId: created.session.id,
      attachmentId: attachment.id,
      caller: attached,
    })
    expect(resumed.attachment.status).toBe("connected")
    expect(resumed.attachmentToken).not.toBe(created.attachmentToken)
    expect(await authService.authenticate(created.attachmentToken)).toBeNull()
    expect(await authService.authenticate(resumed.attachmentToken)).toMatchObject({ scope: "attachment" })

    const resumedCaller = await authService.authenticate(resumed.attachmentToken)
    if (resumedCaller == null) throw new Error("expected rotated attachment caller")
    expect((await service.getSession(created.session.id, resumedCaller)).ownerAbsenceDeadline).toBeNull()
    const refreshed = await service.refreshAttachmentCredential({
      sessionId: created.session.id,
      attachmentId: attachment.id,
      caller: resumedCaller,
    })
    expect(refreshed.attachmentToken).not.toBe(resumed.attachmentToken)
    expect(await authService.authenticate(resumed.attachmentToken)).toBeNull()
  })

  it("does not let an account credential resume or refresh a closed session", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const attachment = created.session.attachments[0]
    const attached = await authService.authenticate(created.attachmentToken)
    if (attachment == null || attached == null) throw new Error("expected owner attachment")

    await expect(service.resumeAttachment({
      sessionId: created.session.id,
      attachmentId: attachment.id,
      caller: owner,
    })).rejects.toBeInstanceOf(SessionForbiddenError)

    await service.closeSession(created.session.id)
    await expect(service.resumeAttachment({ sessionId: created.session.id, attachmentId: attachment.id, caller: attached }))
      .rejects.toBeInstanceOf(SessionInvalidRequestError)
    await expect(service.refreshAttachmentCredential({ sessionId: created.session.id, attachmentId: attachment.id, caller: attached }))
      .rejects.toBeInstanceOf(SessionInvalidRequestError)
  })

  it("rolls back credential rotation when reconnecting the attachment fails", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const attachment = created.session.attachments[0]
    const caller = await authService.authenticate(created.attachmentToken)
    if (attachment == null || caller == null) throw new Error("expected owner attachment")

    await service.disconnectAttachment({ sessionId: created.session.id, attachmentId: attachment.id, caller })
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_owner_resume
      BEFORE UPDATE OF status ON main.attachments
      WHEN NEW.id = '${attachment.id}' AND NEW.status = 'connected'
      BEGIN
        SELECT RAISE(ABORT, 'injected reconnect failure');
      END
    `)

    try {
      await expect(service.resumeAttachment({ sessionId: created.session.id, attachmentId: attachment.id, caller }))
        .rejects.toThrow("injected reconnect failure")
      expect(await authService.authenticate(created.attachmentToken)).toMatchObject({
        scope: "attachment",
        attachmentId: attachment.id,
      })
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_owner_resume")
    }

    const retryCaller = await authService.authenticate(created.attachmentToken)
    if (retryCaller == null) throw new Error("expected original credential after rollback")
    await expect(service.resumeAttachment({ sessionId: created.session.id, attachmentId: attachment.id, caller: retryCaller }))
      .resolves.toMatchObject({ attachment: { id: attachment.id, status: "connected" } })
  })

  it("leaves the owner absence deadline untouched when a non-owner disconnects", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const observer = createBoundObserver({ db, authService, sessionId: created.session.id })
    const observerCaller = await authService.authenticate(observer.token)
    if (observerCaller == null) throw new Error("expected observer attachment caller")

    const disconnected = await service.disconnectAttachment({
      sessionId: created.session.id,
      attachmentId: observer.attachmentId,
      caller: observerCaller,
    })

    expect(disconnected.ownerAbsenceDeadline).toBeNull()
    expect(disconnected.attachments).toContainEqual(expect.objectContaining({
      id: observer.attachmentId,
      status: "disconnected",
    }))
  })

  it("keeps the owner attachment immutable while allowing owner moderation of another attachment", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedUser(db, "foreign-admin", "admin")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const foreignAdmin = await accountCaller(authService, "foreign-admin")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const ownerAttachment = created.session.attachments[0]
    if (ownerAttachment == null) throw new Error("expected owner attachment")
    const observer = createBoundObserver({ db, authService, sessionId: created.session.id })

    await expect(service.promoteAttachment({ sessionId: created.session.id, attachmentId: ownerAttachment.id, caller: owner }))
      .rejects.toBeInstanceOf(SessionInvalidRequestError)
    await expect(service.demoteAttachment({ sessionId: created.session.id, attachmentId: ownerAttachment.id, caller: owner }))
      .rejects.toBeInstanceOf(SessionInvalidRequestError)
    await expect(service.promoteAttachment({ sessionId: created.session.id, attachmentId: observer.attachmentId, caller: foreignAdmin }))
      .rejects.toBeInstanceOf(SessionForbiddenError)
    await expect(service.demoteAttachment({ sessionId: created.session.id, attachmentId: observer.attachmentId, caller: foreignAdmin }))
      .rejects.toBeInstanceOf(SessionForbiddenError)

    expect((await service.promoteAttachment({ sessionId: created.session.id, attachmentId: observer.attachmentId, caller: owner })).role)
      .toBe("participant")
    expect((await service.demoteAttachment({ sessionId: created.session.id, attachmentId: observer.attachmentId, caller: owner })).role)
      .toBe("observer")
  })

  it("audits owner role changes, forced disconnect, and session closure", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const observer = createBoundObserver({ db, authService, sessionId: created.session.id })

    await service.promoteAttachment({
      sessionId: created.session.id,
      attachmentId: observer.attachmentId,
      caller: owner,
      requestId: "request-role",
    })
    await service.disconnectAttachment({
      sessionId: created.session.id,
      attachmentId: observer.attachmentId,
      caller: owner,
      requestId: "request-disconnect",
    })
    await service.closeSession(created.session.id, {
      actorUserId: owner.userId,
      actorCredentialId: owner.credentialId,
      requestId: "request-close",
    })

    expect(listAuditEvents(db, { limit: 20, cursor: null }).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "session.role.change", targetId: observer.attachmentId, requestId: "request-role", outcome: "success" }),
      expect.objectContaining({ action: "session.attachment.disconnect", targetId: observer.attachmentId, requestId: "request-disconnect", outcome: "success" }),
      expect.objectContaining({ action: "session.close", targetId: created.session.id, requestId: "request-close", outcome: "success" }),
    ]))
  })

  it("revokes a kicked attachment before marking its transport for teardown", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000, now })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const observer = createBoundObserver({ db, authService, sessionId: created.session.id })

    await service.disconnectAttachment({
      sessionId: created.session.id,
      attachmentId: observer.attachmentId,
      caller: owner,
    })

    expect(await authService.authenticate(observer.token)).toBeNull()
    expect(getAttachmentById(db, observer.attachmentId)).toMatchObject({
      status: "disconnected",
      transportTeardownPending: true,
    })
  })

  it("tears down every transport after durable session closure", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const destroyed: Array<{ sessionId: string; attachmentId: string }> = []
    const destroyAttachmentTransport = async (input: { sessionId: string; attachmentId: string }) => {
      destroyed.push(input)
    }
    const service = createSessionService({
      db,
      authService,
      defaultOwnerAbsenceTimeoutMs: 300_000,
      now,
      destroyAttachmentTransport,
    })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })

    await service.closeSession(created.session.id)

    const attachment = created.session.attachments[0]
    if (attachment == null) throw new Error("expected owner attachment")
    expect(destroyed).toEqual([{ sessionId: created.session.id, attachmentId: attachment.id }])
    expect(getAttachmentById(db, attachment.id)).toMatchObject({
      status: "disconnected",
      transportTeardownPending: false,
    })
  })

  it("does not extend a repeated owner disconnect deadline", async () => {
    const db = createTestDb()
    let currentTime = new Date("2026-10-02T12:00:00.000Z")
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now: () => currentTime })
    const service = createSessionService({
      db,
      authService,
      defaultOwnerAbsenceTimeoutMs: 300_000,
      now: () => currentTime,
    })
    const owner = await accountCaller(authService, "owner")
    const created = await service.createSession({ instanceId: "instance", ownerClientId: "owner-client", caller: owner })
    const attachment = created.session.attachments[0]
    const caller = await authService.authenticate(created.attachmentToken)
    if (attachment == null || caller == null) throw new Error("expected owner attachment")

    const first = await service.disconnectAttachment({ sessionId: created.session.id, attachmentId: attachment.id, caller })
    currentTime = new Date("2026-10-02T12:03:00.000Z")
    const repeated = await service.disconnectAttachment({ sessionId: created.session.id, attachmentId: attachment.id, caller })

    expect(first.ownerAbsenceDeadline).toBe("2026-10-02T12:05:00.000Z")
    expect(repeated.ownerAbsenceDeadline).toBe("2026-10-02T12:05:00.000Z")
  })

  it("clamps configured owner absence timeouts to its bounds", async () => {
    const db = createTestDb()
    seedUser(db, "owner")
    seedInstance(db)
    const authService = createAuthService({ db, now })
    const service = createSessionService({
      db,
      authService,
      defaultOwnerAbsenceTimeoutMs: 300_000,
      ownerAbsenceTimeoutMinMs: 30_000,
      ownerAbsenceTimeoutMaxMs: 3_600_000,
      now,
    })
    const owner = await accountCaller(authService, "owner")
    const tooLow = await service.createSession({
      instanceId: "instance",
      ownerClientId: "owner-low",
      caller: owner,
      ownerAbsenceTimeoutMs: 1_000,
    })
    const tooLowAttachment = tooLow.session.attachments[0]
    const tooLowCaller = await authService.authenticate(tooLow.attachmentToken)
    if (tooLowAttachment == null || tooLowCaller == null) throw new Error("expected low timeout owner attachment")
    expect((await service.disconnectAttachment({
      sessionId: tooLow.session.id,
      attachmentId: tooLowAttachment.id,
      caller: tooLowCaller,
    })).ownerAbsenceDeadline).toBe("2026-10-02T12:00:30.000Z")

    await service.closeSession(tooLow.session.id)
    const tooHigh = await service.createSession({
      instanceId: "instance",
      ownerClientId: "owner-high",
      caller: owner,
      ownerAbsenceTimeoutMs: 10_000_000,
    })
    const tooHighAttachment = tooHigh.session.attachments[0]
    const tooHighCaller = await authService.authenticate(tooHigh.attachmentToken)
    if (tooHighAttachment == null || tooHighCaller == null) throw new Error("expected high timeout owner attachment")
    expect((await service.disconnectAttachment({
      sessionId: tooHigh.session.id,
      attachmentId: tooHighAttachment.id,
      caller: tooHighCaller,
    })).ownerAbsenceDeadline).toBe("2026-10-02T13:00:00.000Z")
  })
})
