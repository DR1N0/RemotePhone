import { mkdtempSync, rmSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { describe, expect, it } from "vitest"

import { createAppDb } from "../src/persistence/db.js"
import { createAttachment, listAttachmentsForSession } from "../src/persistence/repositories/attachments.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { closeSession, createSession } from "../src/persistence/repositories/sessions.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createSessionInviteService } from "../src/services/session-invite-service.js"
import { SessionConflictError, createSessionService } from "../src/services/session-service.js"

function seedOwnerInstance(db: ReturnType<typeof createAppDb>): void {
  createUser(db, {
    id: "owner-user",
    displayName: "Owner",
    userType: "registered",
    username: "owner",
    passwordHash: "hash",
    enrollmentRequired: false,
    isServerOwner: false,
    status: "active",
  })
  createInstance(db, {
    id: "instance-one",
    ownerId: "owner-user",
    name: "phone",
    runtimeClass: "android-studio-emulator",
    templateId: "template",
    placementPolicy: { kind: "local-only" },
    resourceProfile: { cpuMillicores: 1000, memoryMb: 4096, diskMb: 8192, accelerationMode: "auto", streamProfile: "prototype" },
    launchConfig: { headless: true, readOnly: true, wipeData: false },
    desiredState: "running",
    observedState: "running",
    runtimeHandle: null,
    stateDataRef: null,
    lastError: null,
  })
}

function accountCaller() {
  return { userId: "owner-user", credentialId: "owner-account", scope: "account" as const, sessionId: null, attachmentId: null }
}

describe("session services across independent SQLite connections", () => {
  it("permits exactly one active Session when a second connection commits before the immediate create transaction", async () => {
    const directory = mkdtempSync(join(tmpdir(), "mako-session-create-"))
    const databasePath = join(directory, "controlplane.db")
    const firstDb = createAppDb(databasePath)
    const secondDb = createAppDb(databasePath)
    try {
      seedOwnerInstance(firstDb)
      const secondAuth = createAuthService({ db: secondDb })
      const secondService = createSessionService({ db: secondDb, authService: secondAuth, defaultOwnerAbsenceTimeoutMs: 300_000 })
      const firstAuth = createAuthService({ db: firstDb })
      let secondCreated = false
      const firstService = createSessionService({
        db: firstDb,
        authService: firstAuth,
        defaultOwnerAbsenceTimeoutMs: 300_000,
        beforeCreateSessionTransaction: async () => {
          await secondService.createSession({ instanceId: "instance-one", ownerClientId: "second-client", caller: accountCaller() })
          secondCreated = true
        },
      })

      await expect(firstService.createSession({ instanceId: "instance-one", ownerClientId: "first-client", caller: accountCaller() }))
        .rejects.toBeInstanceOf(SessionConflictError)
      expect(secondCreated).toBe(true)
      const sessions = firstDb.$client.prepare("SELECT id, status FROM sessions WHERE instance_id = ?").all("instance-one") as Array<{ id: string; status: string }>
      expect(sessions).toEqual([expect.objectContaining({ status: "active" })])
      const sessionId = sessions[0]?.id
      expect(sessionId).toBeTruthy()
      const attachments = listAttachmentsForSession(firstDb, sessionId!)
      expect(attachments).toEqual([expect.objectContaining({ role: "owner", userId: "owner-user", clientId: "second-client" })])
      const credential = firstDb.$client.prepare("SELECT scope, user_id, session_id, attachment_id FROM credentials WHERE session_id = ?").get(sessionId) as {
        scope: string; user_id: string; session_id: string; attachment_id: string
      }
      expect(credential).toMatchObject({ scope: "attachment", user_id: "owner-user", session_id: sessionId, attachment_id: attachments[0]?.id })
    } finally {
      firstDb.$client.close()
      secondDb.$client.close()
      rmSync(directory, { recursive: true, force: true })
    }
  })

  it("does not issue an invite or success audit after another connection closes before the recheck", async () => {
    const directory = mkdtempSync(join(tmpdir(), "mako-session-invite-"))
    const databasePath = join(directory, "controlplane.db")
    const firstDb = createAppDb(databasePath)
    const secondDb = createAppDb(databasePath)
    try {
      seedOwnerInstance(firstDb)
      createSession(firstDb, { id: "session-one", instanceId: "instance-one", status: "active" })
      createAttachment(firstDb, { id: "owner-attachment", sessionId: "session-one", userId: "owner-user", clientId: "owner-client", role: "owner", status: "connected" })
      const authService = createAuthService({ db: firstDb })
      const inviteService = createSessionInviteService({
        db: firstDb,
        authService,
        beforeCreateSessionInviteTransaction: async () => {
          closeSession(secondDb, { id: "session-one" })
        },
      })

      await expect(inviteService.createSessionInvite({
        sessionId: "session-one",
        actorUserId: "owner-user",
        actorCredentialId: "owner-account",
        requestId: "cross-connection-close",
      })).rejects.toThrow("session is closed")
      expect(firstDb.$client.prepare("SELECT COUNT(*) AS count FROM session_invites").get()).toEqual({ count: 0 })
      expect(firstDb.$client.prepare("SELECT COUNT(*) AS count FROM audit_events WHERE action = 'session.invite.issue'").get()).toEqual({ count: 0 })
    } finally {
      firstDb.$client.close()
      secondDb.$client.close()
      rmSync(directory, { recursive: true, force: true })
    }
  })
})
