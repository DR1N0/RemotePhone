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
    startInstance: vi.fn(),
    inspectInstance: vi.fn(),
    stopInstance: vi.fn(),
  }
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
