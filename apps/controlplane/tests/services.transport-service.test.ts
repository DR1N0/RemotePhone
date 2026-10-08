import { describe, expect, it, vi } from "vitest"

import type { InstanceService } from "../src/services/instance-service.js"
import type { SessionService } from "../src/services/session-service.js"
import {
  TransportUnavailableError,
  createTransportService,
} from "../src/services/transport-service.js"
import {
  LocalWorkerClient,
  type ManagedInstanceWorkerClient,
  type WorkerTransportSession,
} from "../src/services/worker-client.js"

const sessionId = "session-1"
const attachmentId = "attachment-1"
const instanceId = "instance-1"
const testAudit = { actorUserId: null, actorCredentialId: null, requestId: "request-transport-test" }

const transport: WorkerTransportSession = {
  sessionId,
  attachmentId,
  instanceId,
  runtimeClass: "android-studio-emulator",
  mode: "webrtc",
  status: "ready",
  workerBaseUrl: "http://127.0.0.1:7780",
  sessionToken: "token-1",
}

function createDependencies(options?: {
  attachmentStatus?: "connected" | "disconnected"
  role?: "owner" | "participant" | "observer"
  restrictivePendingRole?: "observer" | null
  pendingTargetRole?: "participant" | "observer" | null
  sessionStatus?: "active" | "closed"
  runtimeHandle?: object | null
  transportTeardownPending?: boolean
}) {
  const sessionService = {
    getSessionUnchecked: vi.fn().mockResolvedValue({
      id: sessionId,
      instanceId,
      status: options?.sessionStatus ?? "active",
      ownerAbsenceDeadline: null,
      reconnectToken: null,
      attachments: [{
        id: attachmentId,
        sessionId,
        clientId: "client-1",
        role: options?.role ?? "observer",
        restrictivePendingRole: options?.restrictivePendingRole ?? null,
        pendingTargetRole: options?.pendingTargetRole ?? null,
        status: options?.attachmentStatus ?? "connected",
        transportTeardownPending: options?.transportTeardownPending ?? false,
        reconnectToken: null,
        createdAt: "2026-09-18T00:00:00.000Z",
        updatedAt: "2026-09-18T00:00:00.000Z",
      }],
      createdAt: "2026-09-18T00:00:00.000Z",
      updatedAt: "2026-09-18T00:00:00.000Z",
    }),
    completeAttachmentTransportTeardown: vi.fn().mockResolvedValue(undefined),
  } as unknown as SessionService
  const instanceService = {
    getManagedInstanceUnchecked: vi.fn().mockResolvedValue({
      id: instanceId,
      runtimeClass: "android-studio-emulator",
      runtimeHandle: options?.runtimeHandle === undefined
        ? { workerId: "local", pid: 4321 }
        : options.runtimeHandle,
    }),
  } as unknown as InstanceService
  const workerClient = {
    createTransportSession: vi.fn().mockResolvedValue(transport),
    updateTransportRole: vi.fn().mockResolvedValue(undefined),
    destroyTransportSession: vi.fn().mockResolvedValue(undefined),
  } as unknown as ManagedInstanceWorkerClient

  return { instanceService, sessionService, workerClient }
}

describe("transport service", () => {
  it("creates a role-aware Worker transport for a connected running attachment", async () => {
    const deps = createDependencies()
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit }))
      .resolves.toEqual(transport)
    expect(deps.workerClient.createTransportSession).toHaveBeenCalledWith({
      sessionId,
      attachmentId,
      instanceId,
      runtimeClass: "android-studio-emulator",
      runtimeHandle: { workerId: "local", pid: 4321 },
      role: "observer",
    })
  })

  it("destroys stale pending transport before recreating a resumed attachment transport", async () => {
    const deps = createDependencies({ transportTeardownPending: true })
    const transportService = createTransportService(deps)

    await transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit })

    expect(deps.workerClient.destroyTransportSession).toHaveBeenCalledWith({ instanceId, attachmentId })
    expect(deps.sessionService.completeAttachmentTransportTeardown).toHaveBeenCalledWith({ sessionId, attachmentId, audit: testAudit })
    expect(deps.workerClient.destroyTransportSession.mock.invocationCallOrder[0])
      .toBeLessThan(deps.workerClient.createTransportSession.mock.invocationCallOrder[0] ?? Number.POSITIVE_INFINITY)
  })

  it("uses the restrictive pending observer role for ensure and default synchronization", async () => {
    const deps = createDependencies({
      role: "participant",
      restrictivePendingRole: "observer",
    })
    const transportService = createTransportService(deps)

    await transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit })
    await transportService.syncAttachmentRole({ sessionId, attachmentId, audit: testAudit })

    expect(deps.workerClient.createTransportSession).toHaveBeenCalledWith(
      expect.objectContaining({ role: "observer" }),
    )
    expect(deps.workerClient.updateTransportRole).toHaveBeenCalledWith({
      instanceId,
      attachmentId,
      role: "observer",
    })
  })

  it("fails closed when only a pending target role is present", async () => {
    const deps = createDependencies({
      role: "participant",
      pendingTargetRole: "participant",
    })
    const transportService = createTransportService(deps)

    await transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit })

    expect(deps.workerClient.createTransportSession).toHaveBeenCalledWith(
      expect.objectContaining({ role: "observer" }),
    )
  })

  it("rejects disconnected attachments without calling the Worker", async () => {
    const deps = createDependencies({ attachmentStatus: "disconnected" })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit }))
      .rejects.toThrow("attachment is not connected")
    expect(deps.workerClient.createTransportSession).not.toHaveBeenCalled()
  })

  it("rejects a session that cannot be resumed without calling the Worker", async () => {
    const deps = createDependencies({ sessionStatus: "closed" })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit }))
      .rejects.toThrow("session is not transportable")
    expect(deps.workerClient.createTransportSession).not.toHaveBeenCalled()
  })

  it("rejects instances without a runtime handle without calling the Worker", async () => {
    const deps = createDependencies({ runtimeHandle: null })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit }))
      .rejects.toThrow("instance is not running")
    expect(deps.workerClient.createTransportSession).not.toHaveBeenCalled()
  })

  it("maps Worker create failures to a transport-unavailable error", async () => {
    const deps = createDependencies()
    deps.workerClient.createTransportSession.mockRejectedValueOnce(new Error("Worker request failed with status 503"))
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit: testAudit }))
      .rejects.toThrow(TransportUnavailableError)
  })

  it("records transport create failure without a fabricated success", async () => {
    const deps = createDependencies()
    deps.workerClient.createTransportSession.mockRejectedValueOnce(new Error("worker unavailable"))
    const auditService = { record: vi.fn() }
    const transportService = createTransportService({ ...deps, auditService })
    const audit = { actorUserId: "attachment-user", actorCredentialId: "attachment-credential", requestId: "request-transport-create-failure" }

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit }))
      .rejects.toThrow(TransportUnavailableError)

    const events = auditService.record.mock.calls.map(([event]) => event)
    expect(events).toEqual(expect.arrayContaining([
      expect.objectContaining({ action: "transport.create", requestId: audit.requestId, outcome: "attempt" }),
      expect.objectContaining({ action: "transport.create", requestId: audit.requestId, outcome: "failure" }),
    ]))
    expect(events).not.toContainEqual(expect.objectContaining({
      action: "transport.create", requestId: audit.requestId, outcome: "success",
    }))
  })

  it("synchronizes the persisted attachment role with the Worker", async () => {
    const deps = createDependencies()
    const transportService = createTransportService(deps)

    await expect(transportService.syncAttachmentRole({ sessionId, attachmentId, audit: testAudit })).resolves.toBeUndefined()
    expect(deps.workerClient.updateTransportRole).toHaveBeenCalledWith({
      instanceId,
      attachmentId,
      role: "observer",
    })
  })

  it("records correlated Worker create and role-update outcomes without transport payloads", async () => {
    const deps = createDependencies()
    const auditService = { record: vi.fn() }
    const transportService = createTransportService({ ...deps, auditService })
    const audit = { actorUserId: "attachment-user", actorCredentialId: "attachment-credential", requestId: "request-transport-operations" }

    await transportService.ensureAttachmentTransport({ sessionId, attachmentId, audit })
    await transportService.syncAttachmentRole({ sessionId, attachmentId, audit })

    expect(auditService.record.mock.calls.map(([event]) => event)).toEqual(expect.arrayContaining([
      expect.objectContaining({ ...audit, action: "transport.create", targetType: "attachment", targetId: attachmentId, outcome: "attempt" }),
      expect.objectContaining({ ...audit, action: "transport.create", targetType: "attachment", targetId: attachmentId, outcome: "success" }),
      expect.objectContaining({ ...audit, action: "transport.role.update", targetType: "attachment", targetId: attachmentId, outcome: "attempt" }),
      expect.objectContaining({ ...audit, action: "transport.role.update", targetType: "attachment", targetId: attachmentId, outcome: "success" }),
    ]))
    expect(JSON.stringify(auditService.record.mock.calls)).not.toContain("token-1")
  })

  it("records correlated attempt and outcome for Worker transport teardown", async () => {
    const deps = createDependencies({ attachmentStatus: "disconnected" })
    const auditService = { record: vi.fn() }
    const transportService = createTransportService({ ...deps, auditService })
    const audit = { actorUserId: "admin-1", actorCredentialId: "credential-1", requestId: "request-teardown" }

    await transportService.destroyAttachmentTransport({ sessionId, attachmentId, audit })

    expect(auditService.record).toHaveBeenNthCalledWith(1, expect.objectContaining({
      ...audit,
      action: "transport.teardown",
      targetType: "attachment",
      targetId: attachmentId,
      outcome: "attempt",
    }))
    expect(auditService.record).toHaveBeenNthCalledWith(2, expect.objectContaining({
      ...audit,
      action: "transport.teardown",
      targetId: attachmentId,
      outcome: "success",
    }))
  })

  it("destroys the exact Worker transport after an attachment disconnects", async () => {
    const deps = createDependencies({ attachmentStatus: "disconnected" })
    const transportService = createTransportService(deps)

    await expect(transportService.destroyAttachmentTransport({ sessionId, attachmentId, audit: testAudit })).resolves.toBeUndefined()
    expect(deps.workerClient.destroyTransportSession).toHaveBeenCalledWith({ instanceId, attachmentId })
  })

  it("maps Worker role-update and destroy failures to transport-unavailable errors", async () => {
    const roleDeps = createDependencies()
    roleDeps.workerClient.updateTransportRole.mockRejectedValueOnce(new Error("Worker request failed with status 503"))
    const roleService = createTransportService(roleDeps)
    await expect(roleService.syncAttachmentRole({ sessionId, attachmentId, audit: testAudit }))
      .rejects.toThrow(TransportUnavailableError)

    const destroyDeps = createDependencies()
    destroyDeps.workerClient.destroyTransportSession.mockRejectedValueOnce(new Error("Worker request failed with status 503"))
    const destroyService = createTransportService(destroyDeps)
    await expect(destroyService.destroyAttachmentTransport({ sessionId, attachmentId, audit: testAudit }))
      .rejects.toThrow(TransportUnavailableError)
  })
})

describe("local worker client transport lifecycle", () => {
  it("updates transport role and destroys an exact transport session", async () => {
    const fetchImpl = vi.fn()
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    const client = new LocalWorkerClient("http://127.0.0.1:7780", fetchImpl as typeof fetch)

    await expect(client.updateTransportRole({ instanceId, attachmentId, role: "participant" }))
      .resolves.toBeUndefined()
    await expect(client.destroyTransportSession({ instanceId, attachmentId })).resolves.toBeUndefined()

    expect(fetchImpl).toHaveBeenNthCalledWith(
      1,
      "http://127.0.0.1:7780/v1/transport/session/role",
      expect.objectContaining({
        method: "PATCH",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ instanceId, attachmentId, role: "participant" }),
      }),
    )
    expect(fetchImpl).toHaveBeenNthCalledWith(
      2,
      "http://127.0.0.1:7780/v1/transport/session?instanceId=instance-1&attachmentId=attachment-1",
      { method: "DELETE" },
    )
  })
})
