import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment, listAttachmentsForSession } from "../src/persistence/repositories/attachments.js"
import { listAuditEvents } from "../src/persistence/repositories/audit-events.js"
import { getCredentialByTokenHash } from "../src/persistence/repositories/credentials.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { closeSession, createSession } from "../src/persistence/repositories/sessions.js"
import { createUser, getUserById, listUsers } from "../src/persistence/repositories/users.js"
import { createAuthService } from "../src/services/auth-service.js"
import {
  createSessionInviteService,
  SessionInviteInvalidError,
} from "../src/services/session-invite-service.js"
import { hashToken } from "../src/services/token.js"

let currentTime: Date

function seedActiveSession() {
  const db = createTestDb()
  createUser(db, {
    id: "owner-user",
    displayName: "Owner",
    userType: "admin",
    username: "owner",
    passwordHash: "hash",
    enrollmentRequired: false,
    isServerOwner: true,
    status: "active",
  })
  createUser(db, {
    id: "registered-user",
    displayName: "Registered Person",
    userType: "registered",
    username: "registered",
    passwordHash: "hash",
    enrollmentRequired: false,
    isServerOwner: false,
    status: "active",
  })
  createInstance(db, {
    id: "instance-1",
    ownerId: "owner-user",
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
    desiredState: "running",
    observedState: "running",
    runtimeHandle: null,
    stateDataRef: null,
    lastError: null,
  })
  createSession(db, { id: "session-1", instanceId: "instance-1", status: "active" })
  createAttachment(db, {
    id: "owner-attachment",
    sessionId: "session-1",
    userId: "owner-user",
    clientId: "owner-client",
    role: "owner",
    status: "connected",
  })
  return db
}

function createService() {
  const db = seedActiveSession()
  const authService = createAuthService({ db, now: () => currentTime })
  const inviteService = createSessionInviteService({ db, authService, now: () => currentTime })
  return { db, authService, inviteService }
}

describe("session invite service", () => {
  beforeEach(() => {
    resetTestDb()
    currentTime = new Date("2026-10-02T12:00:00.000Z")
  })

  it("stores only the SHA-256 hash for an owner-issued invite", async () => {
    const { db, inviteService } = createService()

    const invite = await inviteService.createSessionInvite({
      sessionId: "session-1",
      actorUserId: "owner-user",
      actorCredentialId: null,
      requestId: "request-hashed-invite",
      ttlMs: 10 * 60_000,
    })

    expect(invite.token).toHaveLength(43)
    expect(invite.expiresAt).toBe("2026-10-02T12:10:00.000Z")
    const row = db.$client.prepare("SELECT token_hash FROM session_invites").get() as { token_hash: string }
    expect(row.token_hash).toBe(hashToken(invite.token))
    expect(row.token_hash).not.toBe(invite.token)
  })

  it("audits invitation issue and redemption without retaining the raw token", async () => {
    const { db, inviteService } = createService()
    const invite = await inviteService.createSessionInvite({
      sessionId: "session-1",
      actorUserId: "owner-user",
      actorCredentialId: null,
      requestId: "request-invite-issue",
    })

    await inviteService.redeemSessionInvite({
      token: invite.token,
      mode: "registered",
      requesterUserId: "registered-user",
      actorCredentialId: null,
      requestId: "request-invite-redeem",
    })

    const events = listAuditEvents(db, { limit: 20, cursor: null }).items
    expect(events).toEqual(expect.arrayContaining([
      expect.objectContaining({
        actorUserId: "owner-user",
        action: "session.invite.issue",
        targetType: "session_invite",
        requestId: "request-invite-issue",
        outcome: "success",
        createdAt: expect.any(String),
      }),
      expect.objectContaining({
        actorUserId: "registered-user",
        action: "session.invite.redeem",
        requestId: "request-invite-redeem",
        outcome: "success",
      }),
    ]))
    expect(JSON.stringify(events)).not.toContain(invite.token)
  })

  it("redeems an invite for an existing registered user with a bound 24-hour attachment credential", async () => {
    const { db, authService, inviteService } = createService()
    const invite = await inviteService.createSessionInvite({
      sessionId: "session-1", actorUserId: "owner-user", actorCredentialId: null, requestId: "request-session-invite",
    })

    const joined = await inviteService.redeemSessionInvite({
      token: invite.token,
      mode: "registered",
      requesterUserId: "registered-user",
      actorCredentialId: null,
      requestId: "request-registered-redeem",
    })

    expect(joined.user).toMatchObject({ id: "registered-user", type: "registered", username: "registered" })
    expect(joined.attachment).toMatchObject({ sessionId: "session-1", userId: "registered-user", role: "observer", status: "connected" })
    const credential = getCredentialByTokenHash(db, hashToken(joined.attachmentToken))
    expect(credential).toMatchObject({
      userId: "registered-user",
      scope: "attachment",
      sessionId: "session-1",
      attachmentId: joined.attachment.id,
      expiresAt: "2026-10-03T12:00:00.000Z",
    })
    expect(await authService.authenticate(joined.attachmentToken)).toMatchObject({
      userId: "registered-user",
      scope: "attachment",
      sessionId: "session-1",
      attachmentId: joined.attachment.id,
    })
  })

  it("creates a separate login-less guest and observer attachment", async () => {
    const { inviteService } = createService()
    const invite = await inviteService.createSessionInvite({
      sessionId: "session-1", actorUserId: "owner-user", actorCredentialId: null, requestId: "request-session-invite",
    })

    const joined = await inviteService.redeemSessionInvite({
      token: invite.token, mode: "guest", actorCredentialId: null, requestId: "request-guest-redeem", displayName: "Visitor",
    })

    expect(joined.user).toMatchObject({ type: "guest", username: null, displayName: "Visitor" })
    expect(getUserById(createTestDb(), joined.user.id)).toMatchObject({ passwordHash: null, enrollmentRequired: false })
    expect(joined.attachment).toMatchObject({ userId: joined.user.id, role: "observer", status: "connected" })
  })

  it("atomically permits only one of concurrent registered and guest redemptions", async () => {
    const { db, inviteService } = createService()
    const invite = await inviteService.createSessionInvite({
      sessionId: "session-1", actorUserId: "owner-user", actorCredentialId: null, requestId: "request-session-invite",
    })

    const outcomes = await Promise.allSettled([
      inviteService.redeemSessionInvite({
        token: invite.token, mode: "registered", requesterUserId: "registered-user", actorCredentialId: null, requestId: "request-concurrent-registered",
      }),
      inviteService.redeemSessionInvite({
        token: invite.token, mode: "guest", actorCredentialId: null, requestId: "request-concurrent-guest", displayName: "Visitor",
      }),
    ])

    expect(outcomes.filter((outcome) => outcome.status === "fulfilled")).toHaveLength(1)
    expect(outcomes.filter((outcome) => outcome.status === "rejected")).toHaveLength(1)
    expect(listAttachmentsForSession(db, "session-1").filter((attachment) => attachment.role === "observer")).toHaveLength(1)
  })

  it("rejects invalid guest display names before consuming an invite or creating state", async () => {
    const { db, inviteService } = createService()
    const invite = await inviteService.createSessionInvite({ sessionId: "session-1", actorUserId: "owner-user", actorCredentialId: null, requestId: "request-display-name" })

    await expect(inviteService.redeemSessionInvite({
      token: invite.token, mode: "guest", actorCredentialId: null, requestId: "request-invalid-name", displayName: " ",
    })).rejects.toThrow(SessionInviteInvalidError)
    await expect(inviteService.redeemSessionInvite({
      token: invite.token, mode: "guest", actorCredentialId: null, requestId: "request-long-name", displayName: "x".repeat(41),
    })).rejects.toThrow(SessionInviteInvalidError)
    expect(listUsers(db)).toHaveLength(2)
    expect(listAttachmentsForSession(db, "session-1")).toHaveLength(1)
    expect(db.$client.prepare("SELECT consumed_at FROM session_invites").get()).toEqual({ consumed_at: null })
  })

  it("does not create a user or attachment for expired or closed-session invites", async () => {
    const { db, inviteService } = createService()
    const expired = await inviteService.createSessionInvite({
      sessionId: "session-1",
      actorUserId: "owner-user",
      actorCredentialId: null,
      requestId: "request-expired-invite",
      ttlMs: 60_000,
    })
    currentTime = new Date("2026-10-02T12:01:00.000Z")

    await expect(inviteService.redeemSessionInvite({ token: expired.token, mode: "guest", actorCredentialId: null, requestId: "request-expired-redeem" }))
      .rejects.toThrow(SessionInviteInvalidError)
    expect(listUsers(db)).toHaveLength(2)
    expect(listAttachmentsForSession(db, "session-1")).toHaveLength(1)

    currentTime = new Date("2026-10-02T12:00:00.000Z")
    const closed = await inviteService.createSessionInvite({
      sessionId: "session-1", actorUserId: "owner-user", actorCredentialId: null, requestId: "request-session-invite",
    })
    closeSession(db, { id: "session-1" })

    await expect(inviteService.redeemSessionInvite({ token: closed.token, mode: "guest", actorCredentialId: null, requestId: "request-closed-redeem" }))
      .rejects.toThrow(SessionInviteInvalidError)
    expect(listUsers(db)).toHaveLength(2)
    expect(listAttachmentsForSession(db, "session-1")).toHaveLength(1)
  })
})
