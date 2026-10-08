import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment, getAttachmentById } from "../src/persistence/repositories/attachments.js"
import { listAuditEvents } from "../src/persistence/repositories/audit-events.js"
import { createCredential, getCredentialById } from "../src/persistence/repositories/credentials.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createSession } from "../src/persistence/repositories/sessions.js"
import { createAuthService } from "../src/services/auth-service.js"
import { countActiveAdmins, createUser, getUserById } from "../src/persistence/repositories/users.js"
import {
  createAdminService,
  LastActiveAdminError,
  NotAdminError,
} from "../src/services/admin-service.js"

function seedUser(
  db: ReturnType<typeof createTestDb>,
  input: { id: string; type: "registered" | "admin" | "guest"; status?: "active" | "banned" },
): void {
  createUser(db, {
    id: input.id,
    displayName: input.id,
    userType: input.type,
    username: input.type === "registered" ? input.id.replace(/-/g, "_") : null,
    passwordHash: null,
    enrollmentRequired: input.type === "registered",
    isServerOwner: input.type === "admin",
    status: input.status ?? "active",
  })
}

describe("admin service", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("allows admins to promote and demote users while keeping legacy ownership synchronized", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "registered_user", type: "registered" })
    const adminService = createAdminService({ db })

    await adminService.setUserType({
      actorUserId: "admin_one", actorCredentialId: null, requestId: "request-promote-legacy", targetUserId: "registered_user", type: "admin",
    })
    expect(getUserById(db, "registered_user")).toMatchObject({ userType: "admin", isServerOwner: true })

    await adminService.setUserType({
      actorUserId: "registered_user", actorCredentialId: null, requestId: "request-demote-legacy", targetUserId: "admin_one", type: "registered",
    })
    expect(getUserById(db, "admin_one")).toMatchObject({ userType: "registered", isServerOwner: false })
  })

  it("records type and ban changes with the authenticated actor and request correlation", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "registered_user", type: "registered" })
    const adminService = createAdminService({ db })

    await adminService.setUserType({
      actorUserId: "admin_one",
      actorCredentialId: null,
      requestId: "request-promote",
      targetUserId: "registered_user",
      type: "admin",
    })
    await adminService.setBanStatus({
      actorUserId: "admin_one",
      actorCredentialId: null,
      requestId: "request-ban",
      targetUserId: "registered_user",
      status: "banned",
    })

    expect(listAuditEvents(db, { limit: 20, cursor: null }).items).toEqual(expect.arrayContaining([
      expect.objectContaining({
        actorUserId: "admin_one",
        actorCredentialId: null,
        action: "user.promote",
        targetType: "user",
        targetId: "registered_user",
        requestId: "request-promote",
        outcome: "success",
        createdAt: expect.any(String),
      }),
      expect.objectContaining({
        actorUserId: "admin_one",
        action: "user.ban",
        targetId: "registered_user",
        requestId: "request-ban",
        outcome: "success",
      }),
    ]))
  })

  it("rolls back a user mutation and its success audit event together", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "registered_user", type: "registered" })
    const adminService = createAdminService({ db })
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_audit_test_update
      BEFORE UPDATE OF user_type ON main.users
      WHEN NEW.id = 'registered_user'
      BEGIN
        SELECT RAISE(ABORT, 'injected update failure');
      END
    `)

    try {
      await expect(adminService.setUserType({
        actorUserId: "admin_one",
        actorCredentialId: null,
        requestId: "request-rollback",
        targetUserId: "registered_user",
        type: "admin",
      })).rejects.toThrow("injected update failure")
      expect(getUserById(db, "registered_user")?.userType).toBe("registered")
      expect(listAuditEvents(db, { limit: 20, cursor: null }).items).not.toContainEqual(expect.objectContaining({
        requestId: "request-rollback",
        outcome: "success",
      }))
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_audit_test_update")
    }
  })

  it("rejects type and ban changes from non-admins", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "registered_user", type: "registered" })
    const adminService = createAdminService({ db })

    await expect(adminService.setUserType({
      actorUserId: "registered_user", actorCredentialId: null, requestId: "request-non-admin-type", targetUserId: "admin_one", type: "registered",
    })).rejects.toThrow(NotAdminError)
    await expect(adminService.setBanStatus({
      actorUserId: "registered_user", actorCredentialId: null, requestId: "request-non-admin-ban", targetUserId: "admin_one", status: "banned",
    })).rejects.toThrow(NotAdminError)
  })

  it("atomically preserves one active admin when the final two admins demote concurrently", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "admin_two", type: "admin" })
    const adminService = createAdminService({ db })

    const results = await Promise.allSettled([
      adminService.setUserType({ actorUserId: "admin_one", actorCredentialId: null, requestId: "request-concurrent-one", targetUserId: "admin_one", type: "registered" }),
      adminService.setUserType({ actorUserId: "admin_two", actorCredentialId: null, requestId: "request-concurrent-two", targetUserId: "admin_two", type: "registered" }),
    ])

    expect(results.filter((result) => result.status === "fulfilled")).toHaveLength(1)
    expect(results.filter((result) => result.status === "rejected")[0]).toMatchObject({ reason: expect.any(LastActiveAdminError) })
    expect(countActiveAdmins(db)).toBe(1)
  })

  it("revokes a guest attachment without assigning a session role to the admin", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "guest_one", type: "guest" })
    createInstance(db, {
      id: "instance_one",
      ownerId: "admin_one",
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
    createSession(db, {
      id: "session_one",
      instanceId: "instance_one",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: null,
    })
    createAttachment(db, {
      id: "guest_attachment",
      sessionId: "session_one",
      userId: "guest_one",
      clientId: "guest-client",
      role: "observer",
      status: "connected",
    })
    const authService = createAuthService({ db })
    const guestToken = authService.issueAttachmentCredential({
      userId: "guest_one",
      sessionId: "session_one",
      attachmentId: "guest_attachment",
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
    })
    const adminService = createAdminService({ db })

    await adminService.revokeGuestAttachment({
      actorUserId: "admin_one",
      actorCredentialId: null,
      requestId: "request-guest-revoke",
      sessionId: "session_one",
      attachmentId: "guest_attachment",
    })

    expect(await authService.authenticate(guestToken)).toBeNull()
    expect(getAttachmentById(db, "guest_attachment")).toMatchObject({
      status: "disconnected",
      role: "observer",
      transportTeardownPending: true,
    })
  })

  it("rolls back account credential revocation when its audit event cannot be recorded", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "registered_user", type: "registered" })
    createCredential(db, {
      id: "admin_credential",
      userId: "admin_one",
      tokenHash: "admin-token-hash",
      deviceLabel: "admin device",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: null,
      revokedAt: null,
    })
    createCredential(db, {
      id: "target_credential",
      userId: "registered_user",
      tokenHash: "target-token-hash",
      deviceLabel: "target device",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: null,
      revokedAt: null,
    })
    const adminService = createAdminService({ db })
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_credential_revoke_audit
      BEFORE INSERT ON main.audit_events
      WHEN NEW.request_id = 'request-revoke-rollback'
      BEGIN
        SELECT RAISE(ABORT, 'injected audit failure');
      END
    `)

    try {
      await expect(adminService.revokeAccountCredential({
        actorUserId: "admin_one",
        actorCredentialId: "admin_credential",
        requestId: "request-revoke-rollback",
        targetUserId: "registered_user",
        credentialId: "target_credential",
      })).rejects.toThrow("injected audit failure")
      expect(getCredentialById(db, "target_credential")?.revokedAt).toBeNull()
      expect(listAuditEvents(db, { limit: 20, cursor: null }).items).not.toContainEqual(expect.objectContaining({
        requestId: "request-revoke-rollback",
      }))
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_credential_revoke_audit")
    }
  })

  it("closes an owner session and revokes every attachment when the owner is banned", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    seedUser(db, { id: "owner_user", type: "registered" })
    seedUser(db, { id: "guest_user", type: "guest" })
    createInstance(db, { id: "instance_one", ownerId: "owner_user", name: "phone", runtimeClass: "android-studio-emulator", templateId: "template", placementPolicy: { kind: "local-only" }, resourceProfile: { cpuMillicores: 1000, memoryMb: 4096, diskMb: 8192, accelerationMode: "auto", streamProfile: "prototype" }, launchConfig: { headless: true, readOnly: true, wipeData: false }, desiredState: "running", observedState: "running", runtimeHandle: null, stateDataRef: null, lastError: null })
    createSession(db, { id: "session_one", instanceId: "instance_one", status: "active" })
    createAttachment(db, { id: "owner_attachment", sessionId: "session_one", userId: "owner_user", clientId: "owner", role: "owner", status: "connected" })
    createAttachment(db, { id: "guest_attachment", sessionId: "session_one", userId: "guest_user", clientId: "guest", role: "observer", status: "connected" })
    const authService = createAuthService({ db })
    const guestToken = authService.issueAttachmentCredential({ userId: "guest_user", sessionId: "session_one", attachmentId: "guest_attachment", expiresAt: new Date(Date.now() + 60_000).toISOString() })

    await createAdminService({ db }).setBanStatus({ actorUserId: "admin_one", actorCredentialId: null, requestId: "request-ban-owner", targetUserId: "owner_user", status: "banned" })

    expect(db.$client.prepare("SELECT status FROM sessions WHERE id = ?").get("session_one")).toEqual({ status: "closed" })
    expect(getAttachmentById(db, "owner_attachment")).toMatchObject({ status: "disconnected", transportTeardownPending: true })
    expect(getAttachmentById(db, "guest_attachment")).toMatchObject({ status: "disconnected", transportTeardownPending: true })
    expect(await authService.authenticate(guestToken)).toBeNull()
    expect(db.$client.prepare("SELECT desired_state FROM instances WHERE id = ?").get("instance_one")).toEqual({ desired_state: "running" })
    expect(listAuditEvents(db, { limit: 20, cursor: null }).items).toEqual(expect.arrayContaining([expect.objectContaining({ action: "session.close", targetId: "session_one" })]))
  })

  it("does not allow the final active admin to be banned", async () => {
    const db = createTestDb()
    seedUser(db, { id: "admin_one", type: "admin" })
    const adminService = createAdminService({ db })

    await expect(adminService.setBanStatus({
      actorUserId: "admin_one", actorCredentialId: null, requestId: "request-final-ban", targetUserId: "admin_one", status: "banned",
    })).rejects.toThrow(LastActiveAdminError)
  })
})
