import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment } from "../src/persistence/repositories/attachments.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createSession } from "../src/persistence/repositories/sessions.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createAdminServer, createClientApiServer } from "../src/server.js"
import { createAdminService } from "../src/services/admin-service.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
import { createAdminAuthFixture, setupAdminInService } from "./helpers/admin-auth.js"

const fixtures: Array<ReturnType<typeof createAdminAuthFixture>> = []
const workerClient = {
  listWorkers: vi.fn().mockResolvedValue([]),
  listTemplates: vi.fn().mockResolvedValue([]),
  startInstance: vi.fn().mockResolvedValue({ state: "starting", handle: { workerId: "local", pid: 1 } }),
  inspectInstance: vi.fn().mockResolvedValue({ state: "running", handle: { workerId: "local", pid: 1 } }),
  stopInstance: vi.fn().mockResolvedValue({ state: "stopped", handle: { workerId: "local", pid: 1 } }),
  createTransportSession: vi.fn(),
  updateTransportRole: vi.fn(),
  destroyTransportSession: vi.fn(),
  forwardOffer: vi.fn(),
  forwardICECandidate: vi.fn(),
}

function buildApps(enableSessionDiagnostic = false) {
  const db = createTestDb()
  const fixture = createAdminAuthFixture()
  fixtures.push(fixture)
  const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
  const instanceService = createInstanceService({ db, workerClient })
  const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
  const deps = {
    db, workerClient, authService, instanceService, sessionService,
    adminService: createAdminService({ db }),
    getClientAddress: () => "127.0.0.1",
    enableSessionDiagnostic,
  }
  return { db, fixture, authService, instanceService, client: createClientApiServer(deps), admin: createAdminServer(deps) }
}

describe("separate administration and client API applications", () => {
  beforeEach(resetTestDb)
  afterEach(() => {
    for (const fixture of fixtures.splice(0)) fixture.cleanup()
  })

  it("never mounts management, bootstrap, worker, documentation, or HTML routes on the client app", async () => {
    const { client, admin } = buildApps()
    for (const path of ["/admin", "/api/v1/admin/users", "/api/v1/auth/setup", "/api/v1/auth/status", "/api/v1/workers", "/docs", "/openapi.json", "/unknown"]) {
      const response = await client.request(path)
      expect(response.status).toBe(404)
      expect(response.headers.get("content-type")).toContain("application/json")
    }
    expect((await admin.request("/api/v1/auth/status")).status).toBe(200)
    expect((await admin.request("/api/v1/workers")).status).toBe(200)
    expect((await admin.request("/openapi.json")).status).toBe(200)
  })

  it("does not globally throttle separate proxied usernames on the client app", async () => {
    const { client } = buildApps()
    const login = async (username: string) => await client.request("/api/v1/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json", "x-forwarded-for": "198.51.100.3" },
      body: JSON.stringify({ username, password: "incorrect-password", deviceLabel: "client" }),
    })
    for (const username of ["one", "two", "three", "four", "five", "six"]) expect((await login(username)).status).toBe(401)
    for (let attempt = 0; attempt < 5; attempt += 1) expect((await login("same_user")).status).toBe(401)
    expect((await login("same_user")).status).toBe(429)
  })

  it("retains the direct-peer login throttle on the local admin app", async () => {
    const { admin } = buildApps()
    const login = async (username: string) => await admin.request("/api/v1/auth/login", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ username, password: "incorrect-password", deviceLabel: "admin" }),
    })
    for (const username of ["one", "two", "three", "four", "five"]) expect((await login(username)).status).toBe(401)
    expect((await login("unrelated_username")).status).toBe(429)
  })

  it("uses shared account credentials but retains client owner-only instance authority", async () => {
    const { db, fixture, authService, client, admin } = buildApps()
    const owner = await setupAdminInService(authService, fixture)
    createUser(db, { id: "registered", displayName: "Registered", userType: "registered", username: "registered", passwordHash: "hash", enrollmentRequired: false, isServerOwner: false, status: "active" })
    for (const [id, ownerId] of [["owned", owner.user.id], ["foreign", "registered"]] as const) {
      createInstance(db, {
        id, ownerId, name: id, runtimeClass: "android-studio-emulator", templateId: "template", placementPolicy: { kind: "local-only" },
        resourceProfile: { cpuMillicores: 1000, memoryMb: 512, diskMb: 1024, accelerationMode: "auto", streamProfile: "prototype" },
        launchConfig: { headless: true, readOnly: true, wipeData: false }, desiredState: "stopped", observedState: "stopped", runtimeHandle: null, stateDataRef: null, lastError: null,
      })
    }
    const headers = { Authorization: `Bearer ${owner.token}` }
    expect((await client.request("/api/v1/instances", { headers })).status).toBe(200)
    expect((await client.request("/api/v1/instances/foreign", { headers })).status).toBe(403)
    expect((await client.request("/api/v1/instances/foreign/start", { method: "POST", headers })).status).toBe(403)
    expect((await admin.request("/api/v1/admin/instances/foreign/start", { method: "POST", headers })).status).toBe(200)
    expect((await client.request("/api/v1/templates")).status).toBe(401)
    expect((await client.request("/api/v1/templates", { headers })).status).toBe(200)
  })

  it("only exposes local diagnostic reads behind the flag and requires attachment bearer", async () => {
    const disabled = buildApps()
    expect((await disabled.admin.request("/api/v1/sessions/session")).status).toBe(404)
    const { db, fixture, authService, admin } = buildApps(true)
    const owner = await setupAdminInService(authService, fixture)
    createInstance(db, { id: "instance", ownerId: owner.user.id, name: "instance", runtimeClass: "android-studio-emulator", templateId: "template", placementPolicy: { kind: "local-only" }, resourceProfile: { cpuMillicores: 1000, memoryMb: 512, diskMb: 1024, accelerationMode: "auto", streamProfile: "prototype" }, launchConfig: { headless: true, readOnly: true, wipeData: false }, desiredState: "stopped", observedState: "stopped", runtimeHandle: null, stateDataRef: null, lastError: null })
    createSession(db, { id: "session", instanceId: "instance", status: "active" })
    createAttachment(db, { id: "attachment", sessionId: "session", userId: owner.user.id, clientId: "client", role: "owner", status: "connected" })
    const attachmentToken = authService.issueAttachmentCredential({ userId: owner.user.id, sessionId: "session", attachmentId: "attachment", expiresAt: new Date(Date.now() + 60_000).toISOString() })
    expect((await admin.request("/api/v1/sessions/session")).status).toBe(401)
    expect((await admin.request("/api/v1/sessions/session", { headers: { Authorization: `Bearer ${owner.token}` } })).status).toBe(403)
    expect((await admin.request("/api/v1/sessions/session", { headers: { Authorization: `Bearer ${attachmentToken}` } })).status).toBe(200)
  })
})
