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
  sessionStatus?: "active" | "idle" | "closed"
  idleExpiresAt?: string | null
  runtimeHandle?: object | null
}) {
  const sessionService = {
    getSession: vi.fn().mockResolvedValue({
      id: sessionId,
      instanceId,
      status: options?.sessionStatus ?? "active",
      idleExpiresAt: options?.idleExpiresAt ?? null,
      reconnectToken: null,
      attachments: [{
        id: attachmentId,
        sessionId,
        clientId: "client-1",
        role: options?.role ?? "observer",
        restrictivePendingRole: options?.restrictivePendingRole ?? null,
        pendingTargetRole: options?.pendingTargetRole ?? null,
        status: options?.attachmentStatus ?? "connected",
        reconnectToken: null,
        createdAt: "2026-09-18T00:00:00.000Z",
        updatedAt: "2026-09-18T00:00:00.000Z",
      }],
      createdAt: "2026-09-18T00:00:00.000Z",
      updatedAt: "2026-09-18T00:00:00.000Z",
    })
  } as unknown as SessionService
  const instanceService = {
    getManagedInstance: vi.fn().mockResolvedValue({
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

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId }))
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

  it("uses the restrictive pending observer role for ensure and default synchronization", async () => {
    const deps = createDependencies({
      role: "participant",
      restrictivePendingRole: "observer",
    })
    const transportService = createTransportService(deps)

    await transportService.ensureAttachmentTransport({ sessionId, attachmentId })
    await transportService.syncAttachmentRole({ sessionId, attachmentId })

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

    await transportService.ensureAttachmentTransport({ sessionId, attachmentId })

    expect(deps.workerClient.createTransportSession).toHaveBeenCalledWith(
      expect.objectContaining({ role: "observer" }),
    )
  })

  it("allows a connected attachment on an unexpired idle session", async () => {
    const deps = createDependencies({
      sessionStatus: "idle",
      idleExpiresAt: new Date(Date.now() + 60_000).toISOString(),
    })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId }))
      .resolves.toEqual(transport)
  })

  it("rejects disconnected attachments without calling the Worker", async () => {
    const deps = createDependencies({ attachmentStatus: "disconnected" })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId }))
      .rejects.toThrow("attachment is not connected")
    expect(deps.workerClient.createTransportSession).not.toHaveBeenCalled()
  })

  it("rejects a session that cannot be resumed without calling the Worker", async () => {
    const deps = createDependencies({ sessionStatus: "closed" })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId }))
      .rejects.toThrow("session is not transportable")
    expect(deps.workerClient.createTransportSession).not.toHaveBeenCalled()
  })

  it("rejects instances without a runtime handle without calling the Worker", async () => {
    const deps = createDependencies({ runtimeHandle: null })
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId }))
      .rejects.toThrow("instance is not running")
    expect(deps.workerClient.createTransportSession).not.toHaveBeenCalled()
  })

  it("maps Worker create failures to a transport-unavailable error", async () => {
    const deps = createDependencies()
    deps.workerClient.createTransportSession.mockRejectedValueOnce(new Error("Worker request failed with status 503"))
    const transportService = createTransportService(deps)

    await expect(transportService.ensureAttachmentTransport({ sessionId, attachmentId }))
      .rejects.toThrow(TransportUnavailableError)
  })

  it("synchronizes the persisted attachment role with the Worker", async () => {
    const deps = createDependencies()
    const transportService = createTransportService(deps)

    await expect(transportService.syncAttachmentRole({ sessionId, attachmentId })).resolves.toBeUndefined()
    expect(deps.workerClient.updateTransportRole).toHaveBeenCalledWith({
      instanceId,
      attachmentId,
      role: "observer",
    })
  })

  it("destroys the exact Worker transport after an attachment disconnects", async () => {
    const deps = createDependencies({ attachmentStatus: "disconnected" })
    const transportService = createTransportService(deps)

    await expect(transportService.destroyAttachmentTransport({ sessionId, attachmentId })).resolves.toBeUndefined()
    expect(deps.workerClient.destroyTransportSession).toHaveBeenCalledWith({ instanceId, attachmentId })
  })

  it("maps Worker role-update and destroy failures to transport-unavailable errors", async () => {
    const roleDeps = createDependencies()
    roleDeps.workerClient.updateTransportRole.mockRejectedValueOnce(new Error("Worker request failed with status 503"))
    const roleService = createTransportService(roleDeps)
    await expect(roleService.syncAttachmentRole({ sessionId, attachmentId }))
      .rejects.toThrow(TransportUnavailableError)

    const destroyDeps = createDependencies()
    destroyDeps.workerClient.destroyTransportSession.mockRejectedValueOnce(new Error("Worker request failed with status 503"))
    const destroyService = createTransportService(destroyDeps)
    await expect(destroyService.destroyAttachmentTransport({ sessionId, attachmentId }))
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
