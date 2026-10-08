import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createInstance, getInstanceById } from "../src/persistence/repositories/instances.js"
import {
  createAttachment,
  getAttachmentById,
  setAttachmentTransportTeardownPending,
} from "../src/persistence/repositories/attachments.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createSession, getSessionById } from "../src/persistence/repositories/sessions.js"
import { runReconcilePass } from "../src/services/reconciler.js"

function seedRunningInstance(db: ReturnType<typeof createTestDb>, id: string): void {
  createUser(db, { id: `${id}_owner`, displayName: "Owner", isServerOwner: false, status: "active" })
  createInstance(db, {
    id,
    ownerId: `${id}_owner`,
    name: `phone-${id}`,
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

describe("reconciler", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("force-closes sessions whose owner-absence deadline has expired without touching instance state", async () => {
    const db = createTestDb()
    seedRunningInstance(db, "inst_1")

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      ownerAbsenceDeadline: "2026-07-11T11:59:00.000Z",
      ownerAbsenceTimeoutMs: null,
      reconnectToken: "reconnect-session-1",
    })
    createAttachment(db, {
      id: "att_owner",
      sessionId: "sess_1",
      clientId: "owner-1",
      role: "owner",
      status: "disconnected",
      reconnectToken: "reconnect-attachment-owner",
    })
    createAttachment(db, {
      id: "att_observer",
      sessionId: "sess_1",
      clientId: "observer-1",
      role: "observer",
      status: "connected",
      reconnectToken: "reconnect-attachment-observer",
    })

    const teardownPendingAttachment = vi.fn().mockImplementation(async (input: { attachmentId: string }) => {
      setAttachmentTransportTeardownPending(db, { id: input.attachmentId, pending: false })
      return true
    })
    const inspectRunningInstance = vi.fn().mockResolvedValue({
      state: "running",
      handle: {
        instanceId: "inst_1",
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
      },
    })

    await runReconcilePass({
      db,
      now: new Date("2026-07-11T12:00:00.000Z"),
      inspectRunningInstance,
      teardownPendingAttachment,
    })

    expect(getSessionById(db, "sess_1")?.status).toBe("closed")
    expect(getAttachmentById(db, "att_observer")?.status).toBe("disconnected")
    expect(getAttachmentById(db, "att_owner")?.status).toBe("disconnected")
    expect(teardownPendingAttachment).toHaveBeenCalledWith({ sessionId: "sess_1", attachmentId: "att_owner" })
    expect(teardownPendingAttachment).toHaveBeenCalledWith({ sessionId: "sess_1", attachmentId: "att_observer" })
    expect(getAttachmentById(db, "att_observer")?.transportTeardownPending).toBe(false)
    expect(inspectRunningInstance).toHaveBeenCalledWith("inst_1")
    expect(getInstanceById(db, "inst_1")?.desiredState).toBe("running")
  })

  it("retries failed pending transport teardown without stopping the instance", async () => {
    const db = createTestDb()
    seedRunningInstance(db, "inst_1")
    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: null,
    })
    createAttachment(db, {
      id: "att_1",
      sessionId: "sess_1",
      clientId: "client-1",
      role: "observer",
      status: "disconnected",
      reconnectToken: null,
    })
    setAttachmentTransportTeardownPending(db, { id: "att_1", pending: true })
    const teardownPendingAttachment = vi.fn().mockRejectedValue(new Error("worker unavailable"))

    await runReconcilePass({
      db,
      now: new Date("2026-07-11T12:00:00.000Z"),
      inspectRunningInstance: vi.fn().mockResolvedValue({ state: "running", handle: getInstanceById(db)?.runtimeHandle }),
      teardownPendingAttachment,
    })

    expect(teardownPendingAttachment).toHaveBeenCalledWith({ sessionId: "sess_1", attachmentId: "att_1" })
    expect(getAttachmentById(db, "att_1")?.transportTeardownPending).toBe(true)
    expect(getInstanceById(db, "inst_1")?.desiredState).toBe("running")
  })

  it("does not close an expiry selected before an owner resumes", async () => {
    const db = createTestDb()
    seedRunningInstance(db, "inst_resume")
    createSession(db, { id: "sess_resume", instanceId: "inst_resume", status: "active", ownerAbsenceDeadline: "2026-07-11T11:59:00.000Z" })
    createAttachment(db, { id: "att_resume", sessionId: "sess_resume", clientId: "owner", role: "owner", status: "disconnected" })
    const teardown = vi.fn()

    await runReconcilePass({
      db,
      now: new Date("2026-07-11T12:00:00.000Z"),
      inspectRunningInstance: vi.fn().mockResolvedValue({ state: "running", handle: getInstanceById(db, "inst_resume")?.runtimeHandle }),
      teardownPendingAttachment: teardown,
      beforeExpiredSessionTransaction: async () => {
        db.$client.prepare("UPDATE sessions SET owner_absence_deadline = NULL WHERE id = ?").run("sess_resume")
        db.$client.prepare("UPDATE attachments SET status = 'connected' WHERE id = ?").run("att_resume")
      },
    })

    expect(getSessionById(db, "sess_resume")).toMatchObject({ status: "active", ownerAbsenceDeadline: null })
    expect(getAttachmentById(db, "att_resume")).toMatchObject({ status: "connected", transportTeardownPending: false })
    expect(teardown).not.toHaveBeenCalled()
  })

  it("reconciles running instances and persists inspected runtime state", async () => {
    const db = createTestDb()
    seedRunningInstance(db, "inst_1")
    seedRunningInstance(db, "inst_2")

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      ownerAbsenceDeadline: null,
      ownerAbsenceTimeoutMs: null,
      reconnectToken: "reconnect-session-1",
    })

    const inspectRunningInstance = vi.fn().mockImplementation(async (instanceId: string) => ({
      state: instanceId === "inst_1" ? "degraded" : "running",
      handle: {
        instanceId,
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: instanceId === "inst_1" ? 9876 : 5432,
        adbPort: instanceId === "inst_1" ? 6601 : 5601,
      },
      lastError: instanceId === "inst_1" ? "worker lagging" : undefined,
    }))

    await runReconcilePass({
      db,
      now: new Date("2026-07-11T12:00:00.000Z"),
      inspectRunningInstance,
    })

    expect(inspectRunningInstance).toHaveBeenCalledWith("inst_1")
    expect(inspectRunningInstance).toHaveBeenCalledWith("inst_2")
    expect(getInstanceById(db, "inst_1")).toMatchObject({
      observedState: "degraded",
      runtimeHandle: expect.objectContaining({
        workerId: "local",
        pid: 9876,
        adbPort: 6601,
      }),
      lastError: "worker lagging",
    })
    expect(getInstanceById(db, "inst_2")).toMatchObject({
      observedState: "running",
      runtimeHandle: expect.objectContaining({
        workerId: "local",
        pid: 5432,
        adbPort: 5601,
      }),
      lastError: null,
    })
  })
})
