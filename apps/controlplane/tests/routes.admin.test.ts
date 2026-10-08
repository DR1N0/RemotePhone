import { readFileSync } from "node:fs"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment, getAttachmentById } from "../src/persistence/repositories/attachments.js"
import { createCredential, getCredentialByTokenHash } from "../src/persistence/repositories/credentials.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createSession } from "../src/persistence/repositories/sessions.js"
import { createUser, getUserById } from "../src/persistence/repositories/users.js"
import { createServer } from "../src/server.js"
import { createAdminService } from "../src/services/admin-service.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
import { hashToken } from "../src/services/token.js"
import { createAdminAuthFixture, setupAdmin } from "./helpers/admin-auth.js"

function createWorkerClient() {
  return {
    listWorkers: vi.fn().mockResolvedValue([]),
    listTemplates: vi.fn().mockResolvedValue([]),
    startInstance: vi.fn(),
    inspectInstance: vi.fn(),
    stopInstance: vi.fn(),
    createTransportSession: vi.fn(),
    updateTransportRole: vi.fn(),
    destroyTransportSession: vi.fn().mockResolvedValue(undefined),
    forwardOffer: vi.fn(),
    forwardICECandidate: vi.fn(),
  }
}
const fixtures: Array<{ cleanup: () => void }> = []

function buildApp() {
  const db = createTestDb()
  const fixture = createAdminAuthFixture()
  fixtures.push(fixture)
  const workerClient = createWorkerClient()
  const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
  const instanceService = createInstanceService({ db, workerClient })
  const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
  const adminService = createAdminService({ db })
  return {
    app: createServer({ workerClient, authService, adminService, instanceService, sessionService, getClientAddress: () => "127.0.0.1" }),
    db,
    fixture,
    authService,
    workerClient,
  }
}

describe("admin routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  afterEach(() => {
    for (const fixture of fixtures.splice(0)) fixture.cleanup()
  })

  it("allows an admin to promote and ban a registered user", async () => {
    const { app, db, fixture, authService } = buildApp()
    const admin = await setupAdmin(app, fixture)
    createUser(db, {
      id: "registered_user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered_user",
      passwordHash: null,
      enrollmentRequired: true,
      isServerOwner: false,
      status: "active",
    })
    const registeredToken = authService.issueAccountCredential("registered_user", "old-client")

    expect((await app.request("/api/v1/admin/users/registered_user/type", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}`, "content-type": "application/json" },
      body: JSON.stringify({ type: "admin" }),
    })).status).toBe(204)
    expect(getUserById(db, "registered_user")).toMatchObject({ userType: "admin", isServerOwner: true })

    expect((await app.request("/api/v1/admin/users/registered_user/ban", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}` },
    })).status).toBe(204)
    expect((await app.request("/api/v1/auth/me", {
      headers: { Authorization: `Bearer ${registeredToken}` },
    })).status).toBe(401)
  })

  it("lets an admin revoke only a guest attachment without granting media access", async () => {
    const { app, db, fixture, workerClient } = buildApp()
    const admin = await setupAdmin(app, fixture)
    createUser(db, {
      id: "guest_user",
      displayName: "Guest",
      userType: "guest",
      username: null,
      passwordHash: null,
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    createInstance(db, {
      id: "instance_one",
      ownerId: admin.user.id,
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
      userId: "guest_user",
      clientId: "guest-client",
      role: "observer",
      status: "connected",
    })

    const res = await app.request("/api/v1/admin/sessions/session_one/attachments/guest_attachment/revoke", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}` },
    })

    expect(res.status).toBe(204)
    expect(getAttachmentById(db, "guest_attachment")).toMatchObject({
      role: "observer",
      status: "disconnected",
      transportTeardownPending: false,
    })
    expect(workerClient.destroyTransportSession).toHaveBeenCalledWith({
      instanceId: "instance_one",
      attachmentId: "guest_attachment",
    })
    expect((await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}`, "content-type": "application/json" },
      body: JSON.stringify({ sessionId: "session_one", attachmentId: "guest_attachment", sdpOffer: "v=0" }),
    })).status).toBe(403)
  })

  it("lists only safe registered and admin account metadata with account-admin authorization", async () => {
    const { app, db, fixture, authService } = buildApp()
    const admin = await setupAdmin(app, fixture)
    createUser(db, {
      id: "registered_user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered_user",
      passwordHash: "private-password-hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    createUser(db, {
      id: "guest_user",
      displayName: "Guest User",
      userType: "guest",
      username: null,
      passwordHash: null,
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const registeredToken = authService.issueAccountCredential("registered_user", "registered-client")

    const adminResponse = await app.request("/api/v1/admin/users?limit=20", {
      headers: { Authorization: `Bearer ${admin.token}` },
    })
    expect(adminResponse.status).toBe(200)
    const body = await adminResponse.json() as { items: Array<Record<string, unknown>>; nextCursor: string | null }
    expect(body.items).toEqual(expect.arrayContaining([
      expect.objectContaining({ id: admin.user.id, type: "admin" }),
      expect.objectContaining({ id: "registered_user", type: "registered", username: "registered_user" }),
    ]))
    expect(body.items).not.toContainEqual(expect.objectContaining({ id: "guest_user" }))
    expect(body.nextCursor).toBeNull()
    expect(JSON.stringify(body)).not.toContain("private-password-hash")
    expect(JSON.stringify(body)).not.toContain("passwordHash")

    expect((await app.request("/api/v1/admin/users?limit=20", {
      headers: { Authorization: `Bearer ${registeredToken}` },
    })).status).toBe(403)
  })

  it("lists and revokes only the target user's account credential with an atomic audit event", async () => {
    const { app, db, fixture } = buildApp()
    const admin = await setupAdmin(app, fixture)
    createUser(db, {
      id: "target_user",
      displayName: "Target User",
      userType: "registered",
      username: "target_user",
      passwordHash: "target-password-hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    createUser(db, {
      id: "other_user",
      displayName: "Other User",
      userType: "registered",
      username: "other_user",
      passwordHash: null,
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    createCredential(db, {
      id: "target_credential",
      userId: "target_user",
      tokenHash: "target-token-hash",
      deviceLabel: "target device",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: null,
      revokedAt: null,
    })
    createCredential(db, {
      id: "other_credential",
      userId: "other_user",
      tokenHash: "other-token-hash",
      deviceLabel: "other device",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: null,
      revokedAt: null,
    })
    createInstance(db, {
      id: "target_instance",
      ownerId: "target_user",
      name: "target phone",
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
      id: "target_session",
      instanceId: "target_instance",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: null,
    })
    createAttachment(db, {
      id: "target_attachment",
      sessionId: "target_session",
      userId: "target_user",
      clientId: "target-client",
      role: "owner",
      status: "connected",
    })
    createCredential(db, {
      id: "attachment_credential",
      userId: "target_user",
      tokenHash: "attachment-token-hash",
      deviceLabel: "attachment",
      scope: "attachment",
      sessionId: "target_session",
      attachmentId: "target_attachment",
      notBefore: null,
      expiresAt: null,
      revokedAt: null,
    })

    const listed = await app.request("/api/v1/admin/users/target_user/credentials", {
      headers: { Authorization: `Bearer ${admin.token}` },
    })
    expect(listed.status).toBe(200)
    const listedBody = await listed.json() as { items: Array<Record<string, unknown>> }
    expect(listedBody.items).toEqual([expect.objectContaining({
      id: "target_credential", deviceLabel: "target device", scope: "account",
    })])
    expect(JSON.stringify(listedBody)).not.toContain("attachment_credential")
    expect(JSON.stringify(listedBody)).not.toContain("target-token-hash")
    expect(JSON.stringify(listedBody)).not.toContain("tokenHash")

    expect((await app.request("/api/v1/admin/users/target_user/credentials/other_credential/revoke", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}` },
    })).status).toBe(404)
    expect((await app.request("/api/v1/admin/users/target_user/credentials/attachment_credential/revoke", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}` },
    })).status).toBe(404)

    expect((await app.request("/api/v1/admin/users/target_user/credentials/target_credential/revoke", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}` },
    })).status).toBe(204)
    expect(getCredentialByTokenHash(db, "target-token-hash")?.revokedAt).toEqual(expect.any(String))
    expect(getCredentialByTokenHash(db, "other-token-hash")?.revokedAt).toBeNull()

    const adminCredential = getCredentialByTokenHash(db, hashToken(admin.token))
    const audit = await app.request("/api/v1/admin/audit?limit=20", {
      headers: { Authorization: `Bearer ${admin.token}` },
    })
    expect(audit.status).toBe(200)
    expect((await audit.json()).items).toContainEqual(expect.objectContaining({
      action: "user.credential.revoke",
      targetType: "credential",
      targetId: "target_credential",
      actorUserId: admin.user.id,
      actorCredentialId: adminCredential?.id,
      requestId: expect.any(String),
      outcome: "success",
    }))
  })

  it("shows administrators cross-owner operational instance metadata without private state", async () => {
    const { app, db, fixture } = buildApp()
    const admin = await setupAdmin(app, fixture)
    createUser(db, {
      id: "instance_owner",
      displayName: "Instance Owner",
      userType: "registered",
      username: "instance_owner",
      passwordHash: null,
      enrollmentRequired: true,
      isServerOwner: false,
      status: "active",
    })
    createInstance(db, {
      id: "other_instance",
      ownerId: "instance_owner",
      name: "private phone",
      runtimeClass: "android-studio-emulator",
      templateId: "template",
      placementPolicy: { kind: "local-only" },
      resourceProfile: { cpuMillicores: 1000, memoryMb: 4096, diskMb: 8192, accelerationMode: "auto", streamProfile: "prototype" },
      launchConfig: { headless: true, readOnly: true, wipeData: false },
      desiredState: "running",
      observedState: "running",
      runtimeHandle: { workerId: "private-worker", pid: 1234 },
      stateDataRef: "private-instance-data",
      lastError: "operational error",
    })

    const response = await app.request("/api/v1/admin/instances", {
      headers: { Authorization: `Bearer ${admin.token}` },
    })
    expect(response.status).toBe(200)
    const body = await response.json() as { items: Array<Record<string, unknown>> }
    expect(body.items).toContainEqual(expect.objectContaining({
      id: "other_instance",
      ownerId: "instance_owner",
      desiredState: "running",
      observedState: "running",
    }))
    expect(JSON.stringify(body)).not.toContain("private-instance-data")
    expect(JSON.stringify(body)).not.toContain("private-worker")
    expect(JSON.stringify(body)).not.toContain("stateDataRef")
    expect(JSON.stringify(body)).not.toContain("runtimeHandle")
  })

  it("rejects a registered user's administration requests", async () => {
    const { app, db, fixture, authService } = buildApp()
    await setupAdmin(app, fixture)
    createUser(db, {
      id: "registered_user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered_user",
      passwordHash: null,
      enrollmentRequired: true,
      isServerOwner: false,
      status: "active",
    })
    const token = authService.issueAccountCredential("registered_user", "client")

    expect((await app.request("/api/v1/admin/users/registered_user/type", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "content-type": "application/json" },
      body: JSON.stringify({ type: "admin" }),
    })).status).toBe(403)
    expect((await app.request("/api/v1/admin/users/registered_user/ban", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    })).status).toBe(403)
  })
})
