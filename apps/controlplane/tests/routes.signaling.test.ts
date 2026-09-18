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

describe("signaling route", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("returns sdpAnswer from worker for a started session attachment", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService })

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

    const createSessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: { "content-type": "application/json" },
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
    }
    const ownerAttachment = createSessionBody.session.attachments[0]
    if (ownerAttachment == null) {
      throw new Error("expected owner attachment")
    }

    const res = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { "content-type": "application/json" },
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
    const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService })

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

    await app.request(`/api/v1/instances/${createInstanceBody.instance.id}/start`, {
      method: "POST",
    })

    const createSessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-1",
      }),
    })
    const createSessionBody = await createSessionRes.json() as { session: { id: string } }

    const res = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        sessionId: createSessionBody.session.id,
        attachmentId: "missing-attachment",
        sdpOffer: "v=0\r\noffer",
      }),
    })

    expect(res.status).toBe(404)
    expect(await res.json()).toEqual({
      error: "attachment not found: missing-attachment",
    })
    expect(workerClient.forwardOffer).not.toHaveBeenCalled()
  })

  describe("POST /api/v1/signaling/ice-candidate", () => {
    async function createAppWithSessionAndAttachment() {
      const db = createTestDb()
      const workerClient = createWorkerClient()
      const instanceService = createInstanceService({ db, workerClient })
      const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
      const app = createServer({ workerClient, instanceService, sessionService })

      const createInstanceRes = await app.request("/api/v1/instances", {
        method: "POST",
        headers: { "content-type": "application/json" },
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
      })

      const createSessionRes = await app.request("/api/v1/sessions", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          instanceId: createInstanceBody.instance.id,
          ownerClientId: "owner-ice",
        }),
      })
      const createSessionBody = await createSessionRes.json() as {
        session: { id: string; attachments: Array<{ id: string; role: string }> }
      }
      const ownerAttachment = createSessionBody.session.attachments[0]
      if (ownerAttachment == null) throw new Error("expected owner attachment")

      return { app, workerClient, instanceId: createInstanceBody.instance.id, sessionId: createSessionBody.session.id, attachment: ownerAttachment }
    }

    it("happy path: returns { status: 'ok' } and calls forwardICECandidate with correct args", async () => {
      const { app, workerClient, instanceId, sessionId, attachment } = await createAppWithSessionAndAttachment()

      const res = await app.request("/api/v1/signaling/ice-candidate", {
        method: "POST",
        headers: { "content-type": "application/json" },
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
      const { app, workerClient, sessionId } = await createAppWithSessionAndAttachment()

      const res = await app.request("/api/v1/signaling/ice-candidate", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({
          sessionId,
          attachmentId: "missing",
          candidate: "candidate:1 1 UDP 2122252543 192.168.1.1 54321 typ host",
        }),
      })

      expect(res.status).toBe(404)
      expect(await res.json()).toEqual({ error: "attachment not found: missing" })
      expect(workerClient.forwardICECandidate).not.toHaveBeenCalled()
    })

    it("does not forward an offer when transport ensure fails", async () => {
      const { app, workerClient, sessionId, attachment } = await createAppWithSessionAndAttachment()
      workerClient.createTransportSession.mockRejectedValueOnce(new Error("worker unavailable"))

      const res = await app.request("/api/v1/signaling/session", {
        method: "POST",
        headers: { "content-type": "application/json" },
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
    const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService })

    const createInstanceRes = await app.request("/api/v1/instances", {
      method: "POST",
      headers: { "content-type": "application/json" },
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

    await app.request(`/api/v1/instances/${createInstanceBody.instance.id}/start`, { method: "POST" })

    const createSessionRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-2",
      }),
    })
    const createSessionBody = await createSessionRes.json() as {
      session: { id: string; attachments: Array<{ id: string; role: string }> }
    }
    const ownerAttachment = createSessionBody.session.attachments[0]
    if (ownerAttachment == null) throw new Error("expected owner attachment")

    const res = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { "content-type": "application/json" },
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
})
