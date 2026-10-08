import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment } from "../src/persistence/repositories/attachments.js"
import { createAuditEvent } from "../src/persistence/repositories/audit-events.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createSession } from "../src/persistence/repositories/sessions.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createServer } from "../src/server.js"
import { createAdminService } from "../src/services/admin-service.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
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
    destroyTransportSession: vi.fn(),
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
  }
}

describe("admin audit routes", () => {
  beforeEach(() => resetTestDb())

  afterEach(() => {
    for (const fixture of fixtures.splice(0)) fixture.cleanup()
  })

  it("returns disjoint ordered audit pages and rejects invalid or unknown cursors", async () => {
    const { app, db, fixture } = buildApp()
    const admin = await setupAdmin(app, fixture)
    for (let index = 1; index <= 5; index += 1) {
      createAuditEvent(db, {
        id: `audit-${index}`,
        actorUserId: admin.user.id,
        actorCredentialId: null,
        action: "instance.start",
        targetType: "instance",
        targetId: `instance-${index}`,
        requestId: `request-${index}`,
        outcome: "success",
        createdAt: `2026-10-02T00:00:0${index}.000Z`,
      })
    }

    const first = await app.request("/api/v1/admin/audit?limit=2", {
      headers: { Authorization: `Bearer ${admin.token}` },
    })
    expect(first.status).toBe(200)
    const firstBody = await first.json() as { items: Array<{ id: string }>; nextCursor: string | null }
    expect(firstBody.items.map((item) => item.id)).toEqual(["audit-5", "audit-4"])
    expect(firstBody.nextCursor).toBe("audit-4")

    const second = await app.request(`/api/v1/admin/audit?limit=2&cursor=${firstBody.nextCursor}`, {
      headers: { Authorization: `Bearer ${admin.token}` },
    })
    expect(second.status).toBe(200)
    const secondBody = await second.json() as { items: Array<{ id: string }>; nextCursor: string | null }
    expect(secondBody.items.map((item) => item.id)).toEqual(["audit-3", "audit-2"])
    expect(secondBody.items.map((item) => item.id)).not.toEqual(expect.arrayContaining(firstBody.items.map((item) => item.id)))

    for (const limit of ["0", "101", "1.5", "not-a-number"]) {
      expect((await app.request(`/api/v1/admin/audit?limit=${limit}`, {
        headers: { Authorization: `Bearer ${admin.token}` },
      })).status).toBe(400)
    }
    expect((await app.request("/api/v1/admin/audit?cursor=unknown&limit=2", {
      headers: { Authorization: `Bearer ${admin.token}` },
    })).status).toBe(400)
  })

  it("requires an admin account credential for every admin listing", async () => {
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
    createInstance(db, {
      id: "admin_instance",
      ownerId: admin.user.id,
      name: "admin phone",
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
      id: "guest_session",
      instanceId: "admin_instance",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: null,
    })
    createAttachment(db, {
      id: "guest_attachment",
      sessionId: "guest_session",
      userId: "guest_user",
      clientId: "guest-client",
      role: "observer",
      status: "connected",
    })
    const registeredToken = authService.issueAccountCredential("registered_user", "registered-client")
    const guestToken = authService.issueAttachmentCredential({
      userId: "guest_user",
      sessionId: "guest_session",
      attachmentId: "guest_attachment",
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
    })

    for (const token of [registeredToken, guestToken]) {
      for (const path of ["/api/v1/admin/users?limit=20", "/api/v1/admin/instances", "/api/v1/admin/audit?limit=20"]) {
        expect((await app.request(path, { headers: { Authorization: `Bearer ${token}` } })).status).toBe(403)
      }
    }
  })
})
