import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createInstanceService } from "../src/services/instance-service.js"

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
    stopInstance: vi.fn().mockImplementation(async (instanceId: string) => ({
      state: "stopped",
      handle: {
        instanceId,
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })),
  }
}

describe("instance routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("creates, lists, gets, starts, stops, and deletes managed instances", async () => {
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({
      db: createTestDb(),
      workerClient,
    })
    const app = createServer({ workerClient, instanceService })

    const createRes = await app.request("/api/v1/instances", {
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

    expect(createRes.status).toBe(201)
    const createdBody = await createRes.json() as { instance: { id: string; runtimeHandle: null } }
    expect(createdBody.instance).toMatchObject({
      name: "phone-1",
      runtimeClass: "android-studio-emulator",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      desiredState: "stopped",
      observedState: "stopped",
      runtimeHandle: null,
    })

    const listRes = await app.request("/api/v1/instances")
    expect(listRes.status).toBe(200)
    const listBody = await listRes.json() as { instances: unknown[] }
    expect(listBody.instances).toHaveLength(1)

    const getRes = await app.request(`/api/v1/instances/${createdBody.instance.id}`)
    expect(getRes.status).toBe(200)
    expect(await getRes.json()).toMatchObject({
      instance: {
        id: createdBody.instance.id,
        name: "phone-1",
      },
    })

    const startRes = await app.request(`/api/v1/instances/${createdBody.instance.id}/start`, {
      method: "POST",
    })
    expect(startRes.status).toBe(200)
    expect(await startRes.json()).toMatchObject({
      instance: {
        id: createdBody.instance.id,
        desiredState: "running",
        observedState: "starting",
        runtimeHandle: {
          instanceId: createdBody.instance.id,
          workerId: "local",
          pid: 4321,
          consolePort: 5600,
          adbPort: 5601,
          grpcPort: 8554,
        },
      },
    })

    const stopRes = await app.request(`/api/v1/instances/${createdBody.instance.id}/stop`, {
      method: "POST",
    })
    expect(stopRes.status).toBe(200)
    expect(await stopRes.json()).toMatchObject({
      instance: {
        id: createdBody.instance.id,
        desiredState: "stopped",
        observedState: "stopped",
        runtimeHandle: null,
      },
    })

    const deleteRes = await app.request(`/api/v1/instances/${createdBody.instance.id}`, {
      method: "DELETE",
    })
    expect(deleteRes.status).toBe(204)

    const listAfterDeleteRes = await app.request("/api/v1/instances")
    expect(listAfterDeleteRes.status).toBe(200)
    expect(await listAfterDeleteRes.json()).toEqual({ instances: [] })
  })

  it("rejects unsupported placement policies", async () => {
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({
      db: createTestDb(),
      workerClient,
    })
    const app = createServer({ workerClient, instanceService })

    const res = await app.request("/api/v1/instances", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        name: "phone-1",
        templateId: "macos-android-studio:Medium_Phone",
        placement: { kind: "best-fit" },
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

    expect(res.status).toBe(400)
    expect(await res.json()).toEqual({
      error: "placement policy kind best-fit is not supported in v1",
    })
  })

  it("returns 404 for a missing managed instance", async () => {
    const workerClient = createWorkerClient()
    const instanceService = createInstanceService({
      db: createTestDb(),
      workerClient,
    })
    const app = createServer({ workerClient, instanceService })

    const res = await app.request("/api/v1/instances/missing-instance")

    expect(res.status).toBe(404)
    expect(await res.json()).toEqual({
      error: "managed instance not found: missing-instance",
    })
  })
})
