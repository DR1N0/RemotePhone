import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
import { runReconcilePass } from "../src/services/reconciler.js"
import { createTransportService } from "../src/services/transport-service.js"
import { createAdminAuthFixture, setupAdmin } from "./helpers/admin-auth.js"

const temporaryFixtures: Array<ReturnType<typeof createAdminAuthFixture>> = []

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

async function claimOwnerAuth(
  app: ReturnType<typeof createServer>,
  fixture: ReturnType<typeof createAdminAuthFixture>,
) {
  const { token } = await setupAdmin(app, fixture)
  return { Authorization: `Bearer ${token}`, "content-type": "application/json" }
}

afterEach(() => {
  for (const fixture of temporaryFixtures.splice(0)) {
    fixture.cleanup()
  }
})

describe("signaling route", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("returns sdpAnswer from worker for a started session attachment", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const fixture = createAdminAuthFixture()
    temporaryFixtures.push(fixture)
    const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })

    const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService, authService, getClientAddress: () => "127.0.0.1" })
    const auth = await claimOwnerAuth(app, fixture)

    const createInstanceRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
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
      headers: auth,
    })
    expect(startInstanceRes.status).toBe(200)

    const createSessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-1",
      }),
    })
    expect(createSessionRes.status).toBe(201)
    const createSessionBody = await createSessionRes.json() as {
      session: {
        id: string
        attachments: Array<{ id: string; role: string }>
      }
      attachmentToken: string
    }
    const ownerAttachment = createSessionBody.session.attachments[0]
    if (ownerAttachment == null) {
      throw new Error("expected owner attachment")
    }

    const res = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { Authorization: `Bearer ${createSessionBody.attachmentToken}`, "content-type": "application/json" },
      body: JSON.stringify({
        sessionId: createSessionBody.session.id,
        attachmentId: ownerAttachment.id,
        sdpOffer: "v=0\r\noffer",
      }),
    })

    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({ sdpAnswer: "v=0\r\nanswer" })
    expect(workerClient.createTransportSession).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: createSessionBody.session.id,
      attachmentId: ownerAttachment.id,
      instanceId: createInstanceBody.instance.id,
      role: "owner",
    }))
    expect(workerClient.createTransportSession.mock.invocationCallOrder[0])
      .toBeLessThan(workerClient.forwardOffer.mock.invocationCallOrder[0] ?? Number.POSITIVE_INFINITY)
    expect(workerClient.forwardOffer).toHaveBeenCalledWith({
      instanceId: createInstanceBody.instance.id,
      attachmentId: ownerAttachment.id,
      sdpOffer: "v=0\r\noffer",
    })
  })

  it("rejects signaling for an attachment that is not part of the session", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const fixture = createAdminAuthFixture()
    temporaryFixtures.push(fixture)
    const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })

    const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService, authService, getClientAddress: () => "127.0.0.1" })
    const auth = await claimOwnerAuth(app, fixture)

    const createInstanceRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
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

    await app.request(`/api/v1/instances/${createInstanceBody.instance.id}/start`, {
      method: "POST",
      headers: auth,
    })

    const createSessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-1",
      }),
    })
    const createSessionBody = await createSessionRes.json() as { session: { id: string }; attachmentToken: string }

    const res = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { Authorization: `Bearer ${createSessionBody.attachmentToken}`, "content-type": "application/json" },
      body: JSON.stringify({
        sessionId: createSessionBody.session.id,
        attachmentId: "missing-attachment",
        sdpOffer: "v=0\r\noffer",
      }),
    })

    expect(res.status).toBe(403)
    expect(await res.json()).toEqual({
      error: "attachment credential does not match signaling target",
    })
    expect(workerClient.forwardOffer).not.toHaveBeenCalled()
  })

  describe("POST /api/v1/signaling/ice-candidate", () => {
    async function createAppWithSessionAndAttachment() {
      const db = createTestDb()
      const workerClient = createWorkerClient()
      const instanceService = createInstanceService({ db, workerClient })
    const fixture = createAdminAuthFixture()
    temporaryFixtures.push(fixture)
    const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })

      const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
      const app = createServer({ workerClient, instanceService, sessionService, authService, getClientAddress: () => "127.0.0.1" })
      const auth = await claimOwnerAuth(app, fixture)

      const createInstanceRes = await app.request("/api/v1/instances", {
        method: "POST",
        headers: auth,
        body: JSON.stringify({
          name: "phone-ice",
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

      await app.request(`/api/v1/instances/${createInstanceBody.instance.id}/start`, {
        method: "POST",
        headers: auth,
      })

      const createSessionRes = await app.request("/api/v1/sessions", {
        method: "POST",
        headers: auth,
        body: JSON.stringify({
          instanceId: createInstanceBody.instance.id,
          ownerClientId: "owner-ice",
        }),
      })
      const createSessionBody = await createSessionRes.json() as {
        session: { id: string; attachments: Array<{ id: string; role: string }> }
        attachmentToken: string
      }
      const ownerAttachment = createSessionBody.session.attachments[0]
      if (ownerAttachment == null) throw new Error("expected owner attachment")

      return {
        app,
        workerClient,
        instanceId: createInstanceBody.instance.id,
        sessionId: createSessionBody.session.id,
        attachment: ownerAttachment,
        attachmentToken: createSessionBody.attachmentToken,
      }
    }

    it("happy path: returns { status: 'ok' } and calls forwardICECandidate with correct args", async () => {
      const { app, workerClient, instanceId, sessionId, attachment, attachmentToken } = await createAppWithSessionAndAttachment()

      const res = await app.request("/api/v1/signaling/ice-candidate", {
        method: "POST",
        headers: { Authorization: `Bearer ${attachmentToken}`, "content-type": "application/json" },
        body: JSON.stringify({
          sessionId,
          attachmentId: attachment.id,
          candidate: "candidate:1 1 UDP 2122252543 192.168.1.1 54321 typ host",
        }),
      })

      expect(res.status).toBe(200)
      expect(await res.json()).toEqual({ status: "ok" })
      expect(workerClient.forwardICECandidate).toHaveBeenCalledWith({
        instanceId,
        attachmentId: attachment.id,
        candidate: "candidate:1 1 UDP 2122252543 192.168.1.1 54321 typ host",
      })
    })

    it("unknown attachment: returns 404", async () => {
      const { app, workerClient, sessionId, attachmentToken } = await createAppWithSessionAndAttachment()

      const res = await app.request("/api/v1/signaling/ice-candidate", {
        method: "POST",
        headers: { Authorization: `Bearer ${attachmentToken}`, "content-type": "application/json" },
        body: JSON.stringify({
          sessionId,
          attachmentId: "missing",
          candidate: "candidate:1 1 UDP 2122252543 192.168.1.1 54321 typ host",
        }),
      })

      expect(res.status).toBe(403)
      expect(await res.json()).toEqual({ error: "attachment credential does not match signaling target" })
      expect(workerClient.forwardICECandidate).not.toHaveBeenCalled()
    })

    it("does not forward an offer when transport ensure fails", async () => {
      const { app, workerClient, sessionId, attachment, attachmentToken } = await createAppWithSessionAndAttachment()
      workerClient.createTransportSession.mockRejectedValueOnce(new Error("worker unavailable"))

      const res = await app.request("/api/v1/signaling/session", {
        method: "POST",
        headers: { Authorization: `Bearer ${attachmentToken}`, "content-type": "application/json" },
        body: JSON.stringify({
          sessionId,
          attachmentId: attachment.id,
          sdpOffer: "v=0\r\noffer",
        }),
      })

      expect(res.status).toBe(502)
      expect(workerClient.forwardOffer).not.toHaveBeenCalled()
    })
  })

  it("POST /api/v1/signaling/session forwards offer with correct instanceId, attachmentId, and sdpOffer", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const fixture = createAdminAuthFixture()
    temporaryFixtures.push(fixture)
    const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })

    const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService, authService, getClientAddress: () => "127.0.0.1" })
    const auth = await claimOwnerAuth(app, fixture)

    const createInstanceRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({
        name: "phone-2",
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

    await app.request(`/api/v1/instances/${createInstanceBody.instance.id}/start`, { method: "POST", headers: auth })

    const createSessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-2",
      }),
    })
    const createSessionBody = await createSessionRes.json() as {
      session: { id: string; attachments: Array<{ id: string; role: string }> }
      attachmentToken: string
    }
    const ownerAttachment = createSessionBody.session.attachments[0]
    if (ownerAttachment == null) throw new Error("expected owner attachment")

    const res = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { Authorization: `Bearer ${createSessionBody.attachmentToken}`, "content-type": "application/json" },
      body: JSON.stringify({
        sessionId: createSessionBody.session.id,
        attachmentId: ownerAttachment.id,
        sdpOffer: "v=0\r\noffer",
      }),
    })

    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({ sdpAnswer: "v=0\r\nanswer" })
    expect(workerClient.createTransportSession).toHaveBeenCalledWith(expect.objectContaining({
      sessionId: createSessionBody.session.id,
      attachmentId: ownerAttachment.id,
      instanceId: createInstanceBody.instance.id,
      role: "owner",
    }))
    expect(workerClient.createTransportSession.mock.invocationCallOrder[0])
      .toBeLessThan(workerClient.forwardOffer.mock.invocationCallOrder[0] ?? Number.POSITIVE_INFINITY)
    expect(workerClient.forwardOffer).toHaveBeenCalledWith({
      instanceId: createInstanceBody.instance.id,
      attachmentId: ownerAttachment.id,
      sdpOffer: "v=0\r\noffer",
    })
  })

  it("requires a bound attachment bearer for both SDP and ICE before Worker calls", async () => {
    const { app, workerClient, sessionId, attachment, attachmentToken, authService, sessionService } = await (async () => {
      const db = createTestDb()
      const workerClient = createWorkerClient()
      const instanceService = createInstanceService({ db, workerClient })
      const fixture = createAdminAuthFixture()
      temporaryFixtures.push(fixture)
      const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
      const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
      const app = createServer({ workerClient, instanceService, sessionService, authService, getClientAddress: () => "127.0.0.1" })
      const auth = await claimOwnerAuth(app, fixture)
      const instance = await app.request("/api/v1/instances", {
        method: "POST",
        headers: auth,
        body: JSON.stringify({
          name: "protected-phone",
          templateId: "macos-android-studio:Medium_Phone",
          placement: { kind: "local-only" },
          resourceProfile: { cpuMillicores: 1000, memoryMb: 4096, diskMb: 8192, accelerationMode: "auto", streamProfile: "prototype" },
          launchConfig: { headless: true, readOnly: true, wipeData: false },
        }),
      })
      const instanceId = (await instance.json() as { instance: { id: string } }).instance.id
      await app.request(`/api/v1/instances/${instanceId}/start`, { method: "POST", headers: auth })
      const session = await app.request("/api/v1/sessions", {
        method: "POST",
        headers: auth,
        body: JSON.stringify({ instanceId, ownerClientId: "protected-owner" }),
      })
      const created = await session.json() as {
        session: { id: string; attachments: Array<{ id: string }> }
        attachmentToken: string
      }
      const attachment = created.session.attachments[0]
      if (attachment == null) throw new Error("expected owner attachment")
      return {
        app,
        workerClient,
        sessionId: created.session.id,
        attachment,
        attachmentToken: created.attachmentToken,
        authService,
        sessionService,
      }
    })()

    for (const request of [
      { path: "/api/v1/signaling/session", body: { sessionId, attachmentId: attachment.id, sdpOffer: "v=0\r\noffer" } },
      { path: "/api/v1/signaling/ice-candidate", body: { sessionId, attachmentId: attachment.id, candidate: "candidate" } },
    ]) {
      const res = await app.request(request.path, {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(request.body),
      })
      expect(res.status).toBe(401)
    }
    const boundHeaders = { Authorization: `Bearer ${attachmentToken}`, "content-type": "application/json" }
    for (const request of [
      { path: "/api/v1/signaling/session", body: { sessionId: "wrong-session", attachmentId: attachment.id, sdpOffer: "v=0" } },
      { path: "/api/v1/signaling/ice-candidate", body: { sessionId: "wrong-session", attachmentId: attachment.id, candidate: "candidate" } },
    ]) {
      expect((await app.request(request.path, {
        method: "POST", headers: boundHeaders, body: JSON.stringify(request.body),
      })).status).toBe(403)
    }

    const caller = await authService.authenticate(attachmentToken)
    if (caller == null) throw new Error("expected attachment caller")
    await sessionService.disconnectAttachment({ sessionId, attachmentId: attachment.id, caller })
    for (const request of [
      { path: "/api/v1/signaling/session", body: { sessionId, attachmentId: attachment.id, sdpOffer: "v=0" } },
      { path: "/api/v1/signaling/ice-candidate", body: { sessionId, attachmentId: attachment.id, candidate: "candidate" } },
    ]) {
      expect((await app.request(request.path, {
        method: "POST", headers: boundHeaders, body: JSON.stringify(request.body),
      })).status).toBe(400)
    }
    expect(workerClient.createTransportSession).not.toHaveBeenCalled()
    expect(workerClient.forwardOffer).not.toHaveBeenCalled()
    expect(workerClient.forwardICECandidate).not.toHaveBeenCalled()
  })

  it("waits for delayed stale teardown before signaling recreates a resumed attachment transport", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const fixture = createAdminAuthFixture()
    temporaryFixtures.push(fixture)
    const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
    const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
    const transportService = createTransportService({ instanceService, sessionService, workerClient })
    const app = createServer({
      workerClient,
      instanceService,
      sessionService,
      authService,
      transportService,
      getClientAddress: () => "127.0.0.1",
    })
    const auth = await claimOwnerAuth(app, fixture)
    const instanceRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({
        name: "race-phone",
        templateId: "macos-android-studio:Medium_Phone",
        placement: { kind: "local-only" },
        resourceProfile: { cpuMillicores: 1000, memoryMb: 4096, diskMb: 8192, accelerationMode: "auto", streamProfile: "prototype" },
        launchConfig: { headless: true, readOnly: true, wipeData: false },
      }),
    })
    const instanceId = (await instanceRes.json() as { instance: { id: string } }).instance.id
    await app.request(`/api/v1/instances/${instanceId}/start`, { method: "POST", headers: auth })
    const sessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: auth,
      body: JSON.stringify({ instanceId, ownerClientId: "race-owner" }),
    })
    const created = await sessionRes.json() as {
      session: { id: string; attachments: Array<{ id: string }> }
      attachmentToken: string
    }
    const attachment = created.session.attachments[0]
    if (attachment == null) throw new Error("expected owner attachment")
    const originalCaller = await authService.authenticate(created.attachmentToken)
    if (originalCaller == null) throw new Error("expected original attachment caller")
    await sessionService.disconnectAttachment({
      sessionId: created.session.id,
      attachmentId: attachment.id,
      caller: originalCaller,
    })
    const resumed = await sessionService.resumeAttachment({
      sessionId: created.session.id,
      attachmentId: attachment.id,
      caller: originalCaller,
    })

    let releaseDestroy: (() => void) | undefined
    const destroyStarted = new Promise<void>((resolve) => {
      workerClient.destroyTransportSession.mockImplementationOnce(async () => {
        resolve()
        await new Promise<void>((release) => { releaseDestroy = release })
      })
    })
    const reconcile = runReconcilePass({
      db,
      now: new Date("2026-10-02T12:00:00.000Z"),
      inspectRunningInstance: workerClient.inspectInstance.mockResolvedValue({
        state: "running",
        handle: { instanceId, templateId: "macos-android-studio:Medium_Phone", runtimeClass: "android-studio-emulator", workerId: "local", pid: 4321 },
      }),
      teardownPendingAttachment: transportService.teardownPendingAttachment,
    })
    await destroyStarted

    const signaling = app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { Authorization: `Bearer ${resumed.attachmentToken}`, "content-type": "application/json" },
      body: JSON.stringify({ sessionId: created.session.id, attachmentId: attachment.id, sdpOffer: "v=0\r\noffer" }),
    })
    await Promise.resolve()
    expect(workerClient.createTransportSession).not.toHaveBeenCalled()

    releaseDestroy?.()
    await reconcile
    const signalingRes = await signaling
    expect(signalingRes.status).toBe(200)
    expect(workerClient.destroyTransportSession.mock.invocationCallOrder[0])
      .toBeLessThan(workerClient.createTransportSession.mock.invocationCallOrder[0] ?? Number.POSITIVE_INFINITY)
  })
})
