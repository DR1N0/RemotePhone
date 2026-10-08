import { beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { listAuditEvents } from "../src/persistence/repositories/audit-events.js"
import {
  createInstanceService,
  InstanceConflictError,
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

function seedUser(
  db: ReturnType<typeof createTestDb>,
  input: {
    id: string
    userType?: "registered" | "admin" | "guest"
    enrollmentRequired?: boolean
    status?: "active" | "banned"
    isServerOwner?: boolean
  },
): void {
  createUser(db, {
    id: input.id,
    displayName: input.id,
    userType: input.userType ?? "registered",
    enrollmentRequired: input.enrollmentRequired ?? false,
    isServerOwner: input.isServerOwner ?? false,
    status: input.status ?? "active",
  })
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
    seedUser(db, { id: "owner_1" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })

    const created = await service.createManagedInstance(baseInput, "owner_1")
    expect(created.ownerId).toBe("owner_1")

    const started = await service.startManagedInstance(created.id, "owner_1", {
      actorUserId: "owner_1", actorCredentialId: null, requestId: "request-lifecycle-start",
    })
    expect(started.observedState).toBe("starting")

    const fetched = await service.getManagedInstance(created.id, "owner_1")
    expect(fetched.id).toBe(created.id)

    const listed = await service.listManagedInstances("owner_1")
    expect(listed.map((i) => i.id)).toEqual([created.id])

    await service.stopManagedInstance(created.id, "owner_1", {
      actorUserId: "owner_1", actorCredentialId: null, requestId: "request-lifecycle-stop",
    })
    await service.deleteManagedInstance(created.id, "owner_1", {
      actorUserId: "owner_1", actorCredentialId: null, requestId: "request-lifecycle-delete",
    })
    await expect(service.getManagedInstance(created.id, "owner_1")).rejects.toBeInstanceOf(InstanceNotFoundError)
  })

  it("rejects a guest, banned, or enrollment-required owner before provisioning", async () => {
    const db = createTestDb()
    seedUser(db, { id: "guest", userType: "guest" })
    seedUser(db, { id: "banned", status: "banned" })
    seedUser(db, { id: "unenrolled", enrollmentRequired: true })
    const workerClient = createWorkerClient()
    const service = createInstanceService({ db, workerClient })

    for (const ownerId of ["guest", "banned", "unenrolled"]) {
      await expect(service.createManagedInstance(baseInput, ownerId)).rejects.toBeInstanceOf(InstanceForbiddenError)
    }

    expect(workerClient.startInstance).not.toHaveBeenCalled()
  })

  it("rejects an unsupported placement policy", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })

    await expect(
      service.createManagedInstance(
        { ...baseInput, placement: { kind: "best-fit" as const } },
        "owner_1",
      ),
    ).rejects.toBeInstanceOf(InvalidRequestError)
  })

  it("allows an enrolled user to create one personal instance and rejects a second concurrent create", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })

    const attempts = await Promise.allSettled([
      service.createManagedInstance(baseInput, "owner_1"),
      service.createManagedInstance({ ...baseInput, name: "second-instance" }, "owner_1"),
    ])

    expect(attempts.filter((attempt) => attempt.status === "fulfilled")).toHaveLength(1)
    expect(attempts.filter((attempt) => attempt.status === "rejected")).toHaveLength(1)
    const rejection = attempts.find((attempt) => attempt.status === "rejected")
    expect(rejection?.status === "rejected" ? rejection.reason : undefined).toBeInstanceOf(InstanceConflictError)
    await expect(service.createManagedInstance(baseInput, "owner_1")).rejects.toBeInstanceOf(InstanceConflictError)
  })

  it("forbids a non-owning, non-admin caller from viewing or managing an instance", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    seedUser(db, { id: "stranger" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })
    const created = await service.createManagedInstance(baseInput, "owner_1")

    await expect(service.getManagedInstance(created.id, "stranger")).rejects.toBeInstanceOf(InstanceForbiddenError)
    await expect(service.startManagedInstance(created.id, "stranger", {
      actorUserId: "stranger", actorCredentialId: null, requestId: "request-forbidden-start",
    })).rejects.toBeInstanceOf(InstanceForbiddenError)
    await expect(service.stopManagedInstance(created.id, "stranger", {
      actorUserId: "stranger", actorCredentialId: null, requestId: "request-forbidden-stop",
    })).rejects.toBeInstanceOf(InstanceForbiddenError)
    await expect(service.deleteManagedInstance(created.id, "stranger", {
      actorUserId: "stranger", actorCredentialId: null, requestId: "request-forbidden-delete",
    })).rejects.toBeInstanceOf(InstanceForbiddenError)

    const listed = await service.listManagedInstances("stranger")
    expect(listed).toEqual([])
  })

  it("records a terminal failure when Worker success cannot be persisted for start or stop", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })
    const created = await service.createManagedInstance(baseInput, "owner_1")
    const startAudit = { actorUserId: "owner_1", actorCredentialId: null, requestId: "request-start-persistence-failure" }
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_instance_lifecycle_start
      BEFORE UPDATE OF desired_state ON main.instances
      WHEN NEW.id = '${created.id}' AND NEW.desired_state = 'running'
      BEGIN SELECT RAISE(ABORT, 'injected start persistence failure'); END
    `)
    try {
      await expect(service.startManagedInstance(created.id, "owner_1", startAudit))
        .rejects.toThrow("injected start persistence failure")
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_instance_lifecycle_start")
    }
    expect(listAuditEvents(db, { limit: 20, cursor: null }).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "instance.start", requestId: startAudit.requestId, outcome: "attempt" }),
      expect.objectContaining({ action: "instance.start", requestId: startAudit.requestId, outcome: "failure" }),
    ]))

    await service.startManagedInstance(created.id, "owner_1", {
      actorUserId: "owner_1", actorCredentialId: null, requestId: "request-start-success",
    })
    const stopAudit = { actorUserId: "owner_1", actorCredentialId: null, requestId: "request-stop-persistence-failure" }
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_instance_lifecycle_stop
      BEFORE UPDATE OF desired_state ON main.instances
      WHEN NEW.id = '${created.id}' AND NEW.desired_state = 'stopped'
      BEGIN SELECT RAISE(ABORT, 'injected stop persistence failure'); END
    `)
    try {
      await expect(service.stopManagedInstance(created.id, "owner_1", stopAudit))
        .rejects.toThrow("injected stop persistence failure")
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_instance_lifecycle_stop")
    }
    expect(listAuditEvents(db, { limit: 30, cursor: null }).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "instance.stop", requestId: stopAudit.requestId, outcome: "attempt" }),
      expect.objectContaining({ action: "instance.stop", requestId: stopAudit.requestId, outcome: "failure" }),
    ]))
  })

  it("records a terminal failure when the success audit insert rolls back a Worker result", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })
    const created = await service.createManagedInstance(baseInput, "owner_1")
    const startAudit = { actorUserId: "owner_1", actorCredentialId: null, requestId: "request-start-audit-failure" }
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_start_audit_success
      BEFORE INSERT ON main.audit_events
      WHEN NEW.action = 'instance.start' AND NEW.outcome = 'success'
      BEGIN SELECT RAISE(ABORT, 'injected start audit failure'); END
    `)
    try {
      await expect(service.startManagedInstance(created.id, "owner_1", startAudit))
        .rejects.toThrow("injected start audit failure")
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_start_audit_success")
    }
    expect(listAuditEvents(db, { limit: 20, cursor: null }).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "instance.start", requestId: startAudit.requestId, outcome: "attempt" }),
      expect.objectContaining({ action: "instance.start", requestId: startAudit.requestId, outcome: "failure" }),
    ]))

    await service.startManagedInstance(created.id, "owner_1", {
      actorUserId: "owner_1", actorCredentialId: null, requestId: "request-start-before-stop-audit-failure",
    })
    const stopAudit = { actorUserId: "owner_1", actorCredentialId: null, requestId: "request-stop-audit-failure" }
    db.$client.exec(`
      CREATE TEMP TRIGGER reject_stop_audit_success
      BEFORE INSERT ON main.audit_events
      WHEN NEW.action = 'instance.stop' AND NEW.outcome = 'success'
      BEGIN SELECT RAISE(ABORT, 'injected stop audit failure'); END
    `)
    try {
      await expect(service.stopManagedInstance(created.id, "owner_1", stopAudit))
        .rejects.toThrow("injected stop audit failure")
    } finally {
      db.$client.exec("DROP TRIGGER IF EXISTS reject_stop_audit_success")
    }
    expect(listAuditEvents(db, { limit: 30, cursor: null }).items).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "instance.stop", requestId: stopAudit.requestId, outcome: "attempt" }),
      expect.objectContaining({ action: "instance.stop", requestId: stopAudit.requestId, outcome: "failure" }),
    ]))
  })

  it("records correlated worker attempt and failure without a success event", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    const workerClient = createWorkerClient()
    workerClient.startInstance.mockRejectedValueOnce(new Error("worker offline"))
    const service = createInstanceService({ db, workerClient })
    const created = await service.createManagedInstance(baseInput, "owner_1")

    await expect(service.startManagedInstance(created.id, "owner_1", {
      actorUserId: "owner_1",
      actorCredentialId: null,
      requestId: "request-worker-failure",
    })).rejects.toThrow("worker offline")

    const events = listAuditEvents(db, { limit: 20, cursor: null }).items
    expect(events).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "instance.start", targetId: created.id, requestId: "request-worker-failure", outcome: "attempt" }),
      expect.objectContaining({ action: "instance.start", targetId: created.id, requestId: "request-worker-failure", outcome: "failure" }),
    ]))
    expect(events).not.toContainEqual(expect.objectContaining({
      action: "instance.start",
      requestId: "request-worker-failure",
      outcome: "success",
    }))
  })

  it("lets an admin manage any instance without relying on legacy isServerOwner", async () => {
    const db = createTestDb()
    seedUser(db, { id: "owner_1" })
    seedUser(db, { id: "admin", userType: "admin", isServerOwner: false })
    const service = createInstanceService({ db, workerClient: createWorkerClient() })
    const created = await service.createManagedInstance(baseInput, "owner_1")

    const fetched = await service.getManagedInstance(created.id, "admin")
    expect(fetched.id).toBe(created.id)
    await service.startManagedInstance(created.id, "admin", {
      actorUserId: "admin", actorCredentialId: null, requestId: "request-admin-start",
    })
    await service.stopManagedInstance(created.id, "admin", {
      actorUserId: "admin", actorCredentialId: null, requestId: "request-admin-stop",
    })

    const listed = await service.listManagedInstances("admin")
    expect(listed.map((i) => i.id)).toEqual([created.id])
  })
})
