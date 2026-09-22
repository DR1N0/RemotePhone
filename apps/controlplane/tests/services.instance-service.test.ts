import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createPrincipal } from "../src/persistence/repositories/principals.js"
import {
  createInstanceService,
  InstanceForbiddenError,
  InstanceNotFoundError,
  InvalidRequestError,
} from "../src/services/instance-service.js"

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

const baseInput = {
  name: "test-instance",
  templateId: "macos-android-studio:Medium_Phone",
  placement: { kind: "local-only" as const },
  resourceProfile: {
    cpuMillicores: 1000,
    memoryMb: 4096,
    diskMb: 8192,
    accelerationMode: "auto" as const,
    streamProfile: "prototype" as const,
  },
  launchConfig: {
    headless: true,
    readOnly: true,
    wipeData: false,
  },
}

describe("instance service", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("runs the full create/start/get/list/stop/delete lifecycle for the owning caller", async () => {
    const db = createTestDb()
    createPrincipal(db, { id: "owner_1", displayName: "Owner", isServerOwner: false, status: "active" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })

    const created = await service.createManagedInstance(baseInput, "owner_1")
    expect(created.ownerId).toBe("owner_1")

    const started = await service.startManagedInstance(created.id, "owner_1")
    expect(started.observedState).toBe("starting")

    const fetched = await service.getManagedInstance(created.id, "owner_1")
    expect(fetched.id).toBe(created.id)

    const listed = await service.listManagedInstances("owner_1")
    expect(listed.map((i) => i.id)).toEqual([created.id])

    await service.stopManagedInstance(created.id, "owner_1")
    await service.deleteManagedInstance(created.id, "owner_1")
    await expect(service.getManagedInstance(created.id, "owner_1")).rejects.toBeInstanceOf(InstanceNotFoundError)
  })

  it("rejects an unsupported placement policy", async () => {
    const db = createTestDb()
    createPrincipal(db, { id: "owner_1", displayName: "Owner", isServerOwner: false, status: "active" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })

    await expect(
      service.createManagedInstance(
        { ...baseInput, placement: { kind: "best-fit" as const } },
        "owner_1",
      ),
    ).rejects.toBeInstanceOf(InvalidRequestError)
  })

  it("forbids a non-owning, non-server-owner caller from viewing or managing an instance", async () => {
    const db = createTestDb()
    createPrincipal(db, { id: "owner_1", displayName: "Owner", isServerOwner: false, status: "active" })
    createPrincipal(db, { id: "stranger", displayName: "Stranger", isServerOwner: false, status: "active" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })
    const created = await service.createManagedInstance(baseInput, "owner_1")

    await expect(service.getManagedInstance(created.id, "stranger")).rejects.toBeInstanceOf(InstanceForbiddenError)
    await expect(service.startManagedInstance(created.id, "stranger")).rejects.toBeInstanceOf(InstanceForbiddenError)
    await expect(service.stopManagedInstance(created.id, "stranger")).rejects.toBeInstanceOf(InstanceForbiddenError)
    await expect(service.deleteManagedInstance(created.id, "stranger")).rejects.toBeInstanceOf(InstanceForbiddenError)

    const listed = await service.listManagedInstances("stranger")
    expect(listed).toEqual([])
  })

  it("lets the server owner view and manage any instance, and list every instance", async () => {
    const db = createTestDb()
    createPrincipal(db, { id: "owner_1", displayName: "Owner", isServerOwner: false, status: "active" })
    createPrincipal(db, { id: "root", displayName: "Root", isServerOwner: true, status: "active" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })
    const created = await service.createManagedInstance(baseInput, "owner_1")

    const fetched = await service.getManagedInstance(created.id, "root")
    expect(fetched.id).toBe(created.id)
    await service.startManagedInstance(created.id, "root")
    await service.stopManagedInstance(created.id, "root")

    const listed = await service.listManagedInstances("root")
    expect(listed.map((i) => i.id)).toEqual([created.id])
  })
})
