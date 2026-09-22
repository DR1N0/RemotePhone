import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"

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

function buildApp() {
  const db = createTestDb()
  const authService = createAuthService({ db })
  const instanceService = createInstanceService({ db, workerClient: createWorkerClient() })
  const app = createServer({
    workerClient: createWorkerClient(),
    instanceService,
    authService,
    getClientAddress: () => "127.0.0.1",
  })
  return { app, db }
}

async function claim(app: ReturnType<typeof buildApp>["app"], displayName = "Owner") {
  const res = await app.request("/api/v1/auth/claim", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ displayName }),
  })
  return await res.json() as { principal: { id: string }; token: string }
}

async function inviteAndRedeem(app: ReturnType<typeof buildApp>["app"], ownerToken: string, displayName = "Guest") {
  const inviteRes = await app.request("/api/v1/auth/pairing-invites", {
    method: "POST",
    headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
    body: JSON.stringify({ ttlMinutes: 30 }),
  })
  const { token: inviteToken } = await inviteRes.json() as { token: string }
  const redeemRes = await app.request("/api/v1/auth/pairing-invites/redeem", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ token: inviteToken, displayName, deviceLabel: "device" }),
  })
  return await redeemRes.json() as { principal: { id: string }; token: string }
}

describe("instance routes", () => {
  beforeEach(() => {
    resetTestDb()
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
    const { token: guestToken } = await inviteAndRedeem(app, ownerToken)

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

  it("lets the server owner manage an instance it does not own", async () => {
    const { app } = buildApp()
    const { token: ownerToken } = await claim(app)
    const { token: guestToken } = await inviteAndRedeem(app, ownerToken)

    const createRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: { Authorization: `Bearer ${guestToken}`, "content-type": "application/json" },
      body: JSON.stringify(baseInput),
    })
    const created = await createRes.json() as { instance: { id: string } }

    const getRes = await app.request(`/api/v1/instances/${created.instance.id}`, {
      headers: { Authorization: `Bearer ${ownerToken}` },
    })
    expect(getRes.status).toBe(200)
  })
})
