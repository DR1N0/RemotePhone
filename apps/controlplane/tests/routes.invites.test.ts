import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"

function createWorkerClient() {
  return {
    listWorkers: vi.fn().mockResolvedValue([
      {
        id: "local",
        kind: "local",
        hostOs: "macos",
        arch: "arm64",
        health: "ok",
        installedRuntimes: [
          {
            runtimeClass: "android-studio-emulator",
            displayName: "Android Studio Emulator",
          },
        ],
      },
    ]),
    listTemplates: vi.fn().mockResolvedValue([
      {
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        displayName: "Medium Phone",
        metadata: {
          avdName: "Medium_Phone",
        },
      },
    ]),
    startInstance: vi.fn().mockImplementation(async (input: { instanceId: string; templateId: string; runtimeClass: string }) => ({
      state: "starting",
      handle: {
        instanceId: input.instanceId,
        templateId: input.templateId,
        runtimeClass: input.runtimeClass,
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })),
    inspectInstance: vi.fn(),
    stopInstance: vi.fn(),
    createTransportSession: vi.fn().mockImplementation(async (input: { sessionId: string; attachmentId: string; instanceId: string; runtimeClass: string }) => ({
      sessionId: input.sessionId,
      attachmentId: input.attachmentId,
      instanceId: input.instanceId,
      runtimeClass: input.runtimeClass,
      mode: "prototype-webrtc",
      status: "stub",
      workerBaseUrl: "http://127.0.0.1:7780",
      sessionToken: `stub-${input.sessionId}-${input.attachmentId}`,
    })),
    updateTransportRole: vi.fn().mockResolvedValue(undefined),
    destroyTransportSession: vi.fn().mockResolvedValue(undefined),
    forwardOffer: vi.fn().mockResolvedValue({ sdpAnswer: "v=0\r\nanswer" }),
    forwardICECandidate: vi.fn().mockResolvedValue(undefined),
  }
}

async function createAppWithSession() {
  const db = createTestDb()
  const workerClient = createWorkerClient()
  const instanceService = createInstanceService({ db, workerClient })
  const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
  const app = createServer({
    workerClient,
    instanceService,
    sessionService,
    db,
    baseUrl: "http://localhost:3000",
  })

  // Create and start an instance
  const createInstanceRes = await app.request("/api/v1/instances", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      name: "phone-1",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      },
      launchConfig: {
        headless: true,
        readOnly: true,
        wipeData: false,
      },
    }),
  })
  const createInstanceBody = await createInstanceRes.json() as { instance: { id: string } }

  const startInstanceRes = await app.request(`/api/v1/instances/${createInstanceBody.instance.id}/start`, {
    method: "POST",
  })
  expect(startInstanceRes.status).toBe(200)

  // Create a session
  const createSessionRes = await app.request("/api/v1/sessions", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      instanceId: createInstanceBody.instance.id,
      ownerClientId: "owner-1",
    }),
  })
  expect(createSessionRes.status).toBe(201)
  const createSessionBody = await createSessionRes.json() as { session: { id: string } }

  return { app, sessionId: createSessionBody.session.id }
}

describe("POST /api/v1/sessions/:id/invites", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("returns an invite URL with a single-use token", async () => {
    const { app, sessionId } = await createAppWithSession()

    const res = await app.request(`/api/v1/sessions/${sessionId}/invites`, {
      method: "POST",
      headers: { "content-type": "application/json" },
    })
    expect(res.status).toBe(201)
    const body = await res.json() as { inviteUrl: string; token: string; attachmentId: string }
    expect(body.token).toMatch(/^[a-f0-9-]+$/)
    expect(body.inviteUrl).toContain(body.token)
    expect(body.attachmentId).toBeDefined()
  })

  it("returns 404 for unknown session", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
    const app = createServer({
      workerClient,
      instanceService,
      sessionService,
      db,
      baseUrl: "http://localhost:3000",
    })

    const res = await app.request("/api/v1/sessions/does-not-exist/invites", {
      method: "POST",
    })
    expect(res.status).toBe(404)
  })
})

describe("GET /session/:id — token redemption", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("redirects redeemed invites to the Admin SPA session route", async () => {
    const { app, sessionId } = await createAppWithSession()

    const inviteRes = await app.request(`/api/v1/sessions/${sessionId}/invites`, { method: "POST" })
    expect(inviteRes.status).toBe(201)
    const { token } = await inviteRes.json() as { token: string }

    const redeemRes = await app.request(`/session/${sessionId}?token=${token}`, {
      redirect: "manual",
    })
    expect(redeemRes.status).toBe(302)
    const location = redeemRes.headers.get("location") ?? ""
    const attachmentId = new URL(location, "http://localhost").searchParams.get("attachmentId")
    expect(attachmentId).not.toBeNull()

    const sessionRes = await app.request(`/api/v1/sessions/${sessionId}`)
    expect(sessionRes.status).toBe(200)
    const sessionBody = await sessionRes.json() as {
      session: { attachments: Array<{ id: string; status: string }> }
    }
    expect(sessionBody.session.attachments).toContainEqual(
      expect.objectContaining({ id: attachmentId, status: "connected" }),
    )

    const adminRedirectRes = await app.request(location, { redirect: "manual" })
    expect(adminRedirectRes.status).toBe(302)
    expect(adminRedirectRes.headers.get("location")).toBe(
      `/admin/session/${sessionId}?attachmentId=${attachmentId}`,
    )
  })

  it("returns 400 when token is invalid", async () => {
    const { app, sessionId } = await createAppWithSession()

    const res = await app.request(`/session/${sessionId}?token=bad-token`)
    expect(res.status).toBe(400)
  })
})
