import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import {
  SessionInvalidRequestError,
  createSessionService,
} from "../src/services/session-service.js"

function seedInstance(db: ReturnType<typeof createTestDb>, id = "inst_1"): void {
  createInstance(db, {
    id,
    name: "phone-1",
    runtimeClass: "android-studio-emulator",
    templateId: "macos-android-studio:Medium_Phone",
    placementPolicy: { kind: "local-only" },
    resourceProfile: {
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    },
    launchConfig: { headless: true, readOnly: true, wipeData: false },
    desiredState: "running",
    observedState: "running",
    runtimeHandle: {
      instanceId: id,
      templateId: "macos-android-studio:Medium_Phone",
      runtimeClass: "android-studio-emulator",
      workerId: "local",
      pid: 4321,
    },
    stateDataRef: null,
    lastError: null,
  })
}

describe("session service", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("marks a session idle when the last attachment disconnects and clears idle state when a new client joins", async () => {
    const db = createTestDb()
    seedInstance(db)
    const service = createSessionService({
      db,
      idleTtlMs: 300_000,
      now: () => new Date("2026-07-11T12:00:00.000Z"),
    })

    const created = await service.createSession({
      instanceId: "inst_1",
      ownerClientId: "owner-1",
    })
    const ownerAttachment = created.attachments[0]
    if (ownerAttachment == null) {
      throw new Error("expected owner attachment")
    }

    const disconnected = await service.disconnectAttachment({
      sessionId: created.id,
      attachmentId: ownerAttachment.id,
    })

    expect(disconnected).toMatchObject({
      status: "idle",
      idleExpiresAt: "2026-07-11T12:05:00.000Z",
      attachments: [
        {
          id: ownerAttachment.id,
          status: "disconnected",
          role: "owner",
        },
      ],
    })

    const rejoined = await service.joinSession({
      sessionId: created.id,
      clientId: "owner-1-reconnect",
    })
    expect(rejoined.role).toBe("observer")

    const session = await service.getSession(created.id)
    expect(session.status).toBe("active")
    expect(session.idleExpiresAt).toBeNull()
  })

  it("resumes a disconnected attachment with its original identity and role", async () => {
    const db = createTestDb()
    seedInstance(db)
    const service = createSessionService({
      db,
      idleTtlMs: 300_000,
      now: () => new Date("2026-07-11T12:00:00.000Z"),
    })

    const created = await service.createSession({
      instanceId: "inst_1",
      ownerClientId: "owner-1",
    })
    const joined = await service.joinSession({
      sessionId: created.id,
      clientId: "participant-1",
    })
    const promoted = await service.promoteAttachment({
      sessionId: created.id,
      attachmentId: joined.id,
    })
    if (promoted.reconnectToken == null) {
      throw new Error("expected reconnect token")
    }

    await service.disconnectAttachment({
      sessionId: created.id,
      attachmentId: promoted.id,
    })

    const resumed = await service.resumeAttachment({
      sessionId: created.id,
      reconnectToken: promoted.reconnectToken,
    })

    expect(resumed).toMatchObject({
      id: promoted.id,
      clientId: "participant-1",
      role: "participant",
      status: "connected",
      reconnectToken: promoted.reconnectToken,
    })

    const session = await service.getSession(created.id)
    expect(session.attachments).toHaveLength(2)
    expect(session.attachments).toEqual(expect.arrayContaining([
      expect.objectContaining({
        id: promoted.id,
        clientId: "participant-1",
        role: "participant",
        status: "connected",
      }),
    ]))
  })

  it("rejects changing the original owner attachment role so the session never loses its owner", async () => {
    const db = createTestDb()
    seedInstance(db)
    const service = createSessionService({
      db,
      idleTtlMs: 300_000,
    })

    const created = await service.createSession({
      instanceId: "inst_1",
      ownerClientId: "owner-1",
    })
    const ownerAttachment = created.attachments[0]
    if (ownerAttachment == null) {
      throw new Error("expected owner attachment")
    }

    await expect(
      service.promoteAttachment({
        sessionId: created.id,
        attachmentId: ownerAttachment.id,
      }),
    ).rejects.toThrow(SessionInvalidRequestError)

    await expect(
      service.demoteAttachment({
        sessionId: created.id,
        attachmentId: ownerAttachment.id,
      }),
    ).rejects.toThrow(SessionInvalidRequestError)

    const session = await service.getSession(created.id)
    expect(session.attachments).toEqual([
      expect.objectContaining({
        id: ownerAttachment.id,
        role: "owner",
        status: "connected",
      }),
    ])
  })

  it("does not extend idle expiry for stale repeated disconnects on an already idle session", async () => {
    const db = createTestDb()
    seedInstance(db)
    let currentTime = new Date("2026-07-11T12:00:00.000Z")
    const service = createSessionService({
      db,
      idleTtlMs: 300_000,
      now: () => currentTime,
    })

    const created = await service.createSession({
      instanceId: "inst_1",
      ownerClientId: "owner-1",
    })
    const ownerAttachment = created.attachments[0]
    if (ownerAttachment == null) {
      throw new Error("expected owner attachment")
    }

    const disconnected = await service.disconnectAttachment({
      sessionId: created.id,
      attachmentId: ownerAttachment.id,
    })
    expect(disconnected.status).toBe("idle")
    expect(disconnected.idleExpiresAt).toBe("2026-07-11T12:05:00.000Z")

    currentTime = new Date("2026-07-11T12:03:00.000Z")
    const repeated = await service.disconnectAttachment({
      sessionId: created.id,
      attachmentId: ownerAttachment.id,
    })

    expect(repeated.status).toBe("idle")
    expect(repeated.idleExpiresAt).toBe("2026-07-11T12:05:00.000Z")
  })

  it("rejects closed sessions for promote, demote, and disconnect mutations", async () => {
    const db = createTestDb()
    seedInstance(db)
    const service = createSessionService({
      db,
      idleTtlMs: 300_000,
    })

    const created = await service.createSession({
      instanceId: "inst_1",
      ownerClientId: "owner-1",
    })
    const joined = await service.joinSession({
      sessionId: created.id,
      clientId: "observer-1",
    })

    await service.closeSession(created.id)

    await expect(
      service.promoteAttachment({
        sessionId: created.id,
        attachmentId: joined.id,
      }),
    ).rejects.toThrow(SessionInvalidRequestError)

    await expect(
      service.demoteAttachment({
        sessionId: created.id,
        attachmentId: joined.id,
      }),
    ).rejects.toThrow(SessionInvalidRequestError)

    await expect(
      service.disconnectAttachment({
        sessionId: created.id,
        attachmentId: joined.id,
      }),
    ).rejects.toThrow(SessionInvalidRequestError)
  })
})
