import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createInstanceService, InvalidRequestError } from "../src/services/instance-service.js"

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

describe("instance service", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("creates, starts, stops, and deletes a managed instance while persisting runtime handle metadata", async () => {
    const workerClient = createWorkerClient()
    const service = createInstanceService({
      db: createTestDb(),
      workerClient,
    })

    const created = await service.createManagedInstance({
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
    })

    expect(created).toMatchObject({
      name: "phone-1",
      runtimeClass: "android-studio-emulator",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      desiredState: "stopped",
      observedState: "stopped",
      runtimeHandle: null,
      lastError: null,
    })

    const started = await service.startManagedInstance(created.id)

    expect(workerClient.startInstance).toHaveBeenCalledWith({
      instanceId: created.id,
      runtimeClass: "android-studio-emulator",
      templateId: "macos-android-studio:Medium_Phone",
      launchConfig: {
        headless: true,
        readOnly: true,
        wipeData: false,
      },
    })
    expect(started).toMatchObject({
      id: created.id,
      desiredState: "running",
      observedState: "starting",
      runtimeHandle: {
        instanceId: created.id,
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })

    await expect(service.getManagedInstance(created.id)).resolves.toMatchObject({
      runtimeHandle: {
        pid: 4321,
        workerId: "local",
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })
    await expect(service.listManagedInstances()).resolves.toHaveLength(1)

    const stopped = await service.stopManagedInstance(created.id)

    expect(workerClient.stopInstance).toHaveBeenCalledWith(created.id)
    expect(stopped).toMatchObject({
      id: created.id,
      desiredState: "stopped",
      observedState: "stopped",
      runtimeHandle: null,
      lastError: null,
    })

    await service.deleteManagedInstance(created.id)

    await expect(service.listManagedInstances()).resolves.toEqual([])
  })

  it("rejects unsupported placement policies before persisting an instance", async () => {
    const workerClient = createWorkerClient()
    const service = createInstanceService({
      db: createTestDb(),
      workerClient,
    })

    await expect(service.createManagedInstance({
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
    })).rejects.toBeInstanceOf(InvalidRequestError)

    await expect(service.listManagedInstances()).resolves.toEqual([])
    expect(workerClient.startInstance).not.toHaveBeenCalled()
  })
})
