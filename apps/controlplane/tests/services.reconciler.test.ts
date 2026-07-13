import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createInstance, getInstanceById } from "../src/persistence/repositories/instances.js"
import { createAttachment } from "../src/persistence/repositories/attachments.js"
import { createSession, getSessionById } from "../src/persistence/repositories/sessions.js"
import { runReconcilePass } from "../src/services/reconciler.js"

function seedRunningInstance(db: ReturnType<typeof createTestDb>, id: string): void {
  createInstance(db, {
    id,
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

  it("stops instances whose idle sessions have expired", async () => {
    const db = createTestDb()
    seedRunningInstance(db, "inst_1")

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "idle",
      idleExpiresAt: "2026-07-11T11:59:00.000Z",
      reconnectToken: "reconnect-session-1",
    })
    createAttachment(db, {
      id: "att_1",
      sessionId: "sess_1",
      clientId: "owner-1",
      role: "owner",
      status: "disconnected",
      reconnectToken: "reconnect-attachment-1",
    })

    const stopManagedInstance = vi.fn().mockResolvedValue(undefined)
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
      stopManagedInstance,
      inspectRunningInstance,
    })

    expect(stopManagedInstance).toHaveBeenCalledWith("inst_1")
    expect(inspectRunningInstance).toHaveBeenCalledWith("inst_1")
    expect(getSessionById(db, "sess_1")?.status).toBe("closed")
  })

  it("reconciles running instances and persists inspected runtime state", async () => {
    const db = createTestDb()
    seedRunningInstance(db, "inst_1")
    seedRunningInstance(db, "inst_2")

    createSession(db, {
      id: "sess_1",
      instanceId: "inst_1",
      status: "active",
      idleExpiresAt: null,
      reconnectToken: "reconnect-session-1",
    })

    const stopManagedInstance = vi.fn().mockResolvedValue(undefined)
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
      stopManagedInstance,
      inspectRunningInstance,
    })

    expect(stopManagedInstance).not.toHaveBeenCalled()
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
