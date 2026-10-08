import { afterEach, beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
import { createAdminAuthFixture, setupAdmin } from "./helpers/admin-auth.js"

function createWorkerClient() {
  return {
    listWorkers: async () => [
      { id: "local", kind: "local" as const, hostOs: "macos", arch: "arm64", health: "ok", installedRuntimes: [] },
    ],
    listTemplates: async () => [
      { templateId: "tmpl_1", runtimeClass: "android-studio-emulator", displayName: "Test Template" },
    ],
    startInstance: async () => ({ state: "starting", handle: { workerId: "worker_1", pid: 1 } }),
    inspectInstance: async () => ({ state: "running", handle: { workerId: "worker_1", pid: 1 } }),
    stopInstance: async () => ({ state: "stopped", handle: { workerId: "worker_1", pid: 1 } }),
    createTransportSession: async () => ({}),
    updateTransportRole: async () => {},
    destroyTransportSession: async () => {},
    forwardOffer: async () => {},
    forwardICECandidate: async () => {},
  }
}

const baseInput = {
  name: "test-instance",
  templateId: "tmpl_1",
  placement: { kind: "local-only" },
  resourceProfile: {
    cpuMillicores: 1000,
    memoryMb: 512,
    diskMb: 4096,
    accelerationMode: "auto",
    streamProfile: "prototype",
  },
  launchConfig: { headless: true, readOnly: true, wipeData: false },
}

const adminAuthFixtures = new WeakMap<object, ReturnType<typeof createAdminAuthFixture>>()
const temporaryFixtures: Array<ReturnType<typeof createAdminAuthFixture>> = []

function buildApp() {
  const db = createTestDb()
  const fixture = createAdminAuthFixture()
  temporaryFixtures.push(fixture)
  const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
  const workerClient = createWorkerClient()
  const instanceService = createInstanceService({ db, workerClient })
  const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
  const app = createServer({
    workerClient,
    instanceService,
    sessionService,
    authService,
    db,
    getClientAddress: () => "127.0.0.1",
  })
  adminAuthFixtures.set(app, fixture)
  return { app, db, authService }
}

async function claim(app: ReturnType<typeof buildApp>["app"]) {
  const fixture = adminAuthFixtures.get(app)
  if (fixture == null) {
    throw new Error("missing admin auth fixture")
  }
  return await setupAdmin(app, fixture)
}

async function registerInvitedUser(app: ReturnType<typeof buildApp>["app"], ownerToken: string, username = "registered_user") {
  const inviteRes = await app.request("/api/v1/auth/registration-invites", {
    method: "POST",
    headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
    body: JSON.stringify({ ttlMs: 30 * 60_000 }),
  })
  const { token: inviteToken } = await inviteRes.json() as { token: string }
  const registerRes = await app.request("/api/v1/auth/register", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ inviteToken, username, password: "long-password-for-test", deviceLabel: "device" }),
  })
  return await registerRes.json() as { user: { id: string }; token: string }
}

describe("instance routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  afterEach(() => {
    for (const fixture of temporaryFixtures.splice(0)) {
      fixture.cleanup()
    }
  })

  it("runs the full create/list/get/start/stop/delete HTTP flow for the owning caller", async () => {
    const { app } = buildApp()
    const { token } = await claim(app)
    const auth = { Authorization: `Bearer ${token}`, "content-type": "application/json" }

    const createRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
      body: JSON.stringify(baseInput),
    })
    expect(createRes.status).toBe(201)
    const created = await createRes.json() as { instance: { id: string } }

    const listRes = await app.request("/api/v1/instances", { headers: auth })
    const listed = await listRes.json() as { instances: { id: string }[] }
    expect(listed.instances.map((i) => i.id)).toEqual([created.instance.id])

    const getRes = await app.request(`/api/v1/instances/${created.instance.id}`, { headers: auth })
    expect(getRes.status).toBe(200)

    const startRes = await app.request(`/api/v1/instances/${created.instance.id}/start`, { method: "POST", headers: auth })
    expect(startRes.status).toBe(200)

    const stopRes = await app.request(`/api/v1/instances/${created.instance.id}/stop`, { method: "POST", headers: auth })
    expect(stopRes.status).toBe(200)

    const deleteRes = await app.request(`/api/v1/instances/${created.instance.id}`, { method: "DELETE", headers: auth })
    expect(deleteRes.status).toBe(204)
  })

  it("rejects an unsupported placement policy with 400", async () => {
    const { app } = buildApp()
    const { token } = await claim(app)

    const res = await app.request("/api/v1/instances", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "content-type": "application/json" },
      body: JSON.stringify({ ...baseInput, placement: { kind: "best-fit" } }),
    })
    expect(res.status).toBe(400)
  })

  it("returns 404 for a missing instance", async () => {
    const { app } = buildApp()
    const { token } = await claim(app)

    const res = await app.request("/api/v1/instances/does-not-exist", {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.status).toBe(404)
  })

  it("returns 401 without a bearer token", async () => {
    const { app } = buildApp()
    const res = await app.request("/api/v1/instances")
    expect(res.status).toBe(401)
  })

  it("forbids a non-owning caller from viewing or managing an instance, and hides it from listing", async () => {
    const { app } = buildApp()
    const { token: ownerToken } = await claim(app)
    const { token: guestToken } = await registerInvitedUser(app, ownerToken)

    const createRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
      body: JSON.stringify(baseInput),
    })
    const created = await createRes.json() as { instance: { id: string } }

    const guestAuth = { Authorization: `Bearer ${guestToken}` }
    const getRes = await app.request(`/api/v1/instances/${created.instance.id}`, { headers: guestAuth })
    expect(getRes.status).toBe(403)

    const startRes = await app.request(`/api/v1/instances/${created.instance.id}/start`, { method: "POST", headers: guestAuth })
    expect(startRes.status).toBe(403)

    const listRes = await app.request("/api/v1/instances", { headers: guestAuth })
    const listed = await listRes.json() as { instances: unknown[] }
    expect(listed.instances).toEqual([])
  })

  it("maps a second personal instance create to 409", async () => {
    const { app } = buildApp()
    const { token } = await claim(app)
    const auth = { Authorization: `Bearer ${token}`, "content-type": "application/json" }

    const first = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
      body: JSON.stringify(baseInput),
    })
    expect(first.status).toBe(201)

    const second = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({ ...baseInput, name: "second-instance" }),
    })
    expect(second.status).toBe(409)
  })

  it("lets an admin operate another user's instance without becoming its session owner", async () => {
    const { app } = buildApp()
    const { token: adminToken } = await claim(app)
    const { token: ownerToken } = await registerInvitedUser(app, adminToken)
    const ownerAuth = { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" }
    const adminAuth = { Authorization: `Bearer ${adminToken}`, "content-type": "application/json" }

    const createRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: ownerAuth,
      body: JSON.stringify(baseInput),
    })
    expect(createRes.status).toBe(201)
    const { instance } = await createRes.json() as { instance: { id: string } }

    expect((await app.request(`/api/v1/instances/${instance.id}`, { headers: adminAuth })).status).toBe(200)
    expect((await app.request(`/api/v1/instances/${instance.id}/start`, { method: "POST", headers: adminAuth })).status).toBe(200)
    expect((await app.request(`/api/v1/instances/${instance.id}/stop`, { method: "POST", headers: adminAuth })).status).toBe(200)

    const sessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: adminAuth,
      body: JSON.stringify({ instanceId: instance.id, ownerClientId: "admin-client" }),
    })
    expect(sessionRes.status).toBe(403)

    expect((await app.request(`/api/v1/instances/${instance.id}`, { method: "DELETE", headers: adminAuth })).status).toBe(204)
  })

  it("rejects attachment-scoped credentials on every instance route", async () => {
    const { app, authService } = buildApp()
    const account = await claim(app)
    const accountAuth = { Authorization: `Bearer ${account.token}`, "content-type": "application/json" }

    const createRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: accountAuth,
      body: JSON.stringify(baseInput),
    })
    expect(createRes.status).toBe(201)
    const { instance } = await createRes.json() as { instance: { id: string } }
    expect((await app.request("/api/v1/instances", { headers: accountAuth })).status).toBe(200)

    const sessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: accountAuth,
      body: JSON.stringify({ instanceId: instance.id, ownerClientId: "owner-client" }),
    })
    expect(sessionRes.status).toBe(201)
    const { session } = await sessionRes.json() as { session: { id: string; attachments: Array<{ id: string }> } }
    const ownerAttachment = session.attachments[0]
    if (ownerAttachment == null) {
      throw new Error("expected owner attachment")
    }
    const attachmentToken = authService.issueAttachmentCredential({
      userId: account.user.id,
      sessionId: session.id,
      attachmentId: ownerAttachment.id,
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
    })
    const attachmentAuth = { Authorization: `Bearer ${attachmentToken}`, "content-type": "application/json" }

    expect((await app.request("/api/v1/instances", {
      method: "POST",
      headers: attachmentAuth,
      body: JSON.stringify({ ...baseInput, name: "attachment-create" }),
    })).status).toBe(403)
    expect((await app.request("/api/v1/instances", { headers: attachmentAuth })).status).toBe(403)
    expect((await app.request(`/api/v1/instances/${instance.id}`, { headers: attachmentAuth })).status).toBe(403)
    expect((await app.request(`/api/v1/instances/${instance.id}/start`, { method: "POST", headers: attachmentAuth })).status).toBe(403)
    expect((await app.request(`/api/v1/instances/${instance.id}/stop`, { method: "POST", headers: attachmentAuth })).status).toBe(403)
    expect((await app.request(`/api/v1/instances/${instance.id}`, { method: "DELETE", headers: attachmentAuth })).status).toBe(403)
  })
})
