import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createServer } from "../src/server.js"
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
    startInstance: vi.fn().mockImplementation(async (input: {
      instanceId: string
      templateId: string
      runtimeClass: string
    }) => ({
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
    createTransportSession: vi.fn().mockImplementation(async (input: {
      sessionId: string
      attachmentId: string
      instanceId: string
      runtimeClass: string
    }) => ({
      sessionId: input.sessionId,
      attachmentId: input.attachmentId,
      instanceId: input.instanceId,
      runtimeClass: input.runtimeClass,
      mode: "webrtc",
      status: "ready",
      workerBaseUrl: "http://127.0.0.1:7780",
      sessionToken: `stub-${input.sessionId}-${input.attachmentId}`,
    })),
    updateTransportRole: vi.fn().mockResolvedValue(undefined),
    destroyTransportSession: vi.fn().mockResolvedValue(undefined),
    forwardOffer: vi.fn(),
    forwardICECandidate: vi.fn(),
  }
}

async function createAppWithObserverAttachment() {
  const db = createTestDb()
  const workerClient = createWorkerClient()
  const instanceService = createInstanceService({ db, workerClient })
  const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
  const app = createServer({ workerClient, instanceService, sessionService })
  const instance = await instanceService.createManagedInstance({
    name: "phone-lifecycle",
    templateId: "macos-android-studio:Medium_Phone",
    placement: { kind: "local-only" },
    resourceProfile: {
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    },
    launchConfig: { headless: true, readOnly: true, wipeData: false },
  })
  await instanceService.startManagedInstance(instance.id)
  const session = await sessionService.createSession({
    instanceId: instance.id,
    ownerClientId: "owner-lifecycle",
  })
  const attachment = await sessionService.joinSession({
    sessionId: session.id,
    clientId: "observer-lifecycle",
  })

  return { app, instance, session, attachment, sessionService, workerClient }
}

describe("session routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("creates a session, joins observers by default, reads session state, and supports promote/demote flows", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({
      db,
      workerClient,
    })
    const sessionService = createSessionService({
      db,
      idleTtlMs: 300_000,
    })
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
        instanceId: string
        status: string
        attachments: Array<{ id: string; clientId: string; role: string; status: string }>
      }
    }
    expect(createSessionBody.session).toMatchObject({
      instanceId: createInstanceBody.instance.id,
      status: "active",
      attachments: [
        {
          clientId: "owner-1",
          role: "owner",
          status: "connected",
        },
      ],
    })

    const joinRes = await app.request(`/api/v1/sessions/${createSessionBody.session.id}/join`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        clientId: "observer-1",
      }),
    })

    expect(joinRes.status).toBe(201)
    const joinBody = await joinRes.json() as {
      attachment: { id: string; clientId: string; role: string; status: string }
    }
    expect(joinBody.attachment).toMatchObject({
      clientId: "observer-1",
      role: "observer",
      status: "connected",
    })

    const readRes = await app.request(`/api/v1/sessions/${createSessionBody.session.id}`)
    expect(readRes.status).toBe(200)
    expect(await readRes.json()).toMatchObject({
      session: {
        id: createSessionBody.session.id,
        status: "active",
        attachments: [
          {
            clientId: "owner-1",
            role: "owner",
          },
          {
            id: joinBody.attachment.id,
            clientId: "observer-1",
            role: "observer",
          },
        ],
      },
    })

    const promoteRes = await app.request(
      `/api/v1/sessions/${createSessionBody.session.id}/attachments/${joinBody.attachment.id}/promote`,
      { method: "POST" },
    )
    expect(promoteRes.status).toBe(200)
    expect(await promoteRes.json()).toMatchObject({
      attachment: {
        id: joinBody.attachment.id,
        role: "participant",
      },
    })
    expect(workerClient.updateTransportRole).toHaveBeenLastCalledWith({
      instanceId: createInstanceBody.instance.id,
      attachmentId: joinBody.attachment.id,
      role: "participant",
    })

    const demoteRes = await app.request(
      `/api/v1/sessions/${createSessionBody.session.id}/attachments/${joinBody.attachment.id}/demote`,
      { method: "POST" },
    )
    expect(demoteRes.status).toBe(200)
    expect(await demoteRes.json()).toMatchObject({
      attachment: {
        id: joinBody.attachment.id,
        role: "observer",
      },
    })
    expect(workerClient.updateTransportRole).toHaveBeenLastCalledWith({
      instanceId: createInstanceBody.instance.id,
      attachmentId: joinBody.attachment.id,
      role: "observer",
    })
  })

  it("uses a restrictive pending role for later offers after promotion synchronization times out", async () => {
    const { app, attachment, session, workerClient } = await createAppWithObserverAttachment()
    workerClient.updateTransportRole.mockRejectedValueOnce(new Error("worker unavailable"))
    workerClient.forwardOffer.mockResolvedValue({ sdpAnswer: "v=0\r\nanswer" })

    const promoteRes = await app.request(
      `/api/v1/sessions/${session.id}/attachments/${attachment.id}/promote`,
      { method: "POST" },
    )

    expect(promoteRes.status).toBe(502)
    expect(workerClient.updateTransportRole).toHaveBeenNthCalledWith(1, {
      instanceId: session.instanceId,
      attachmentId: attachment.id,
      role: "participant",
    })
    expect(workerClient.updateTransportRole).toHaveBeenNthCalledWith(2, {
      instanceId: session.instanceId,
      attachmentId: attachment.id,
      role: "observer",
    })
    const readRes = await app.request(`/api/v1/sessions/${session.id}`)
    const readBody = await readRes.json() as {
      session: {
        attachments: Array<{
          id: string
          role: string
          restrictivePendingRole: string | null
          pendingTargetRole: string | null
          requestedTransportRole: string
          effectiveTransportRole: string
        }>
      }
    }
    expect(readBody.session.attachments).toContainEqual(
      expect.objectContaining({
        id: attachment.id,
        role: "participant",
        restrictivePendingRole: "observer",
        pendingTargetRole: "participant",
        requestedTransportRole: "participant",
        effectiveTransportRole: "observer",
      }),
    )

    const offerRes = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        sessionId: session.id,
        attachmentId: attachment.id,
        sdpOffer: "v=0\r\noffer",
      }),
    })

    expect(offerRes.status).toBe(200)
    expect(workerClient.createTransportSession).toHaveBeenLastCalledWith(expect.objectContaining({
      attachmentId: attachment.id,
      role: "observer",
    }))
  })

  it("restricts the Worker before persisting an attachment demotion", async () => {
    const { app, attachment, session, sessionService, workerClient } = await createAppWithObserverAttachment()
    await sessionService.promoteAttachment({ sessionId: session.id, attachmentId: attachment.id })
    workerClient.updateTransportRole.mockImplementationOnce(async () => {
      const current = await sessionService.getSession(session.id)
      expect(current.attachments).toContainEqual(
        expect.objectContaining({ id: attachment.id, role: "participant" }),
      )
    })

    const demoteRes = await app.request(
      `/api/v1/sessions/${session.id}/attachments/${attachment.id}/demote`,
      { method: "POST" },
    )

    expect(demoteRes.status).toBe(200)
    expect(workerClient.updateTransportRole).toHaveBeenCalledWith({
      instanceId: session.instanceId,
      attachmentId: attachment.id,
      role: "observer",
    })
  })

  it("uses a restrictive pending role for later offers after demotion persistence fails", async () => {
    const { app, attachment, session, sessionService, workerClient } = await createAppWithObserverAttachment()
    await sessionService.promoteAttachment({ sessionId: session.id, attachmentId: attachment.id })
    vi.spyOn(sessionService, "demoteAttachment").mockRejectedValueOnce(new Error("persistence failed"))
    workerClient.forwardOffer.mockResolvedValue({ sdpAnswer: "v=0\r\nanswer" })

    const demoteRes = await app.request(
      `/api/v1/sessions/${session.id}/attachments/${attachment.id}/demote`,
      { method: "POST" },
    )

    expect(demoteRes.status).toBe(500)
    expect(workerClient.updateTransportRole).toHaveBeenCalledWith({
      instanceId: session.instanceId,
      attachmentId: attachment.id,
      role: "observer",
    })
    const readRes = await app.request(`/api/v1/sessions/${session.id}`)
    const readBody = await readRes.json() as {
      session: {
        attachments: Array<{
          id: string
          role: string
          pendingTargetRole: string | null
          requestedTransportRole: string
          effectiveTransportRole: string
        }>
      }
    }
    expect(readBody.session.attachments).toContainEqual(
      expect.objectContaining({
        id: attachment.id,
        role: "participant",
        pendingTargetRole: "observer",
        requestedTransportRole: "observer",
        effectiveTransportRole: "observer",
      }),
    )

    const offerRes = await app.request("/api/v1/signaling/session", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        sessionId: session.id,
        attachmentId: attachment.id,
        sdpOffer: "v=0\r\noffer",
      }),
    })

    expect(offerRes.status).toBe(200)
    expect(workerClient.createTransportSession).toHaveBeenLastCalledWith(expect.objectContaining({
      attachmentId: attachment.id,
      role: "observer",
    }))
  })

  it("disconnects the last attachment through the API and resumes the same attachment", async () => {
    const db = createTestDb()
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({ db, workerClient })
    const sessionService = createSessionService({
      db,
      idleTtlMs: 300_000,
      now: () => new Date("2026-07-11T12:00:00.000Z"),
    })
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
        attachments: Array<{ id: string; clientId: string; role: string; status: string; reconnectToken: string | null }>
      }
    }
    const ownerAttachment = createSessionBody.session.attachments[0]
    if (ownerAttachment == null || ownerAttachment.reconnectToken == null) {
      throw new Error("expected owner reconnect token")
    }

    const disconnectRes = await app.request(
      `/api/v1/sessions/${createSessionBody.session.id}/attachments/${ownerAttachment.id}/disconnect`,
      { method: "POST" },
    )
    expect(disconnectRes.status).toBe(200)
    expect(await disconnectRes.json()).toMatchObject({
      session: {
        id: createSessionBody.session.id,
        status: "idle",
        idleExpiresAt: "2026-07-11T12:05:00.000Z",
        attachments: [
          {
            id: ownerAttachment.id,
            role: "owner",
            status: "disconnected",
          },
        ],
      },
    })
    expect(workerClient.destroyTransportSession).toHaveBeenCalledWith({
      instanceId: createInstanceBody.instance.id,
      attachmentId: ownerAttachment.id,
    })

    const resumeRes = await app.request(`/api/v1/sessions/${createSessionBody.session.id}/resume`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        reconnectToken: ownerAttachment.reconnectToken,
      }),
    })
    expect(resumeRes.status).toBe(200)
    expect(await resumeRes.json()).toMatchObject({
      attachment: {
        id: ownerAttachment.id,
        clientId: "owner-1",
        role: "owner",
        status: "connected",
        reconnectToken: ownerAttachment.reconnectToken,
      },
    })

    const readRes = await app.request(`/api/v1/sessions/${createSessionBody.session.id}`)
    expect(readRes.status).toBe(200)
    const readBody = await readRes.json() as {
      session: {
        status: string
        idleExpiresAt: string | null
        attachments: Array<{ id: string; clientId: string; role: string; status: string }>
      }
    }
    expect(readBody.session.status).toBe("active")
    expect(readBody.session.idleExpiresAt).toBeNull()
    expect(readBody.session.attachments).toHaveLength(1)
    expect(readBody.session.attachments[0]).toMatchObject({
      id: ownerAttachment.id,
      clientId: "owner-1",
      role: "owner",
      status: "connected",
    })
  })

  it("rejects creating a second open session for the same instance", async () => {
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

    const firstRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-1",
      }),
    })
    expect(firstRes.status).toBe(201)

    const secondRes = await app.request("/api/v1/sessions", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        instanceId: createInstanceBody.instance.id,
        ownerClientId: "owner-2",
      }),
    })

    expect(secondRes.status).toBe(409)
    expect(await secondRes.json()).toEqual({
      error: `active session already exists for instance ${createInstanceBody.instance.id}`,
    })
  })
})
