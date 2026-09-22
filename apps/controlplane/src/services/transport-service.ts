import type { SessionRole } from "../domain/types.js"
import type { InstanceService } from "./instance-service.js"
import {
  AttachmentNotFoundError,
  SessionInvalidRequestError,
  type SessionRecord,
  type SessionService,
} from "./session-service.js"
import type {
  ManagedInstanceWorkerClient,
  WorkerTransportSession,
} from "./worker-client.js"

export class TransportUnavailableError extends Error {}

export type TransportService = ReturnType<typeof createTransportService>

type AttachmentTransportInput = {
  sessionId: string
  attachmentId: string
  role?: SessionRole
}

export function effectiveTransportRole(
  attachment: {
    role: SessionRole
    restrictivePendingRole: "observer" | null
    pendingTargetRole: "participant" | "observer" | null
  },
  explicitRole?: SessionRole,
): SessionRole {
  if (explicitRole != null) {
    return explicitRole
  }

  return attachment.restrictivePendingRole != null || attachment.pendingTargetRole != null
    ? "observer"
    : attachment.role
}

function assertTransportableSession(session: SessionRecord): void {
  if (session.status === "active") {
    return
  }

  const idleExpiry = session.idleExpiresAt == null ? Number.NaN : Date.parse(session.idleExpiresAt)
  if (session.status === "idle" && Number.isFinite(idleExpiry) && idleExpiry > Date.now()) {
    return
  }

  throw new SessionInvalidRequestError(`session is not transportable: ${session.id}`)
}

function getRequiredAttachment(session: SessionRecord, attachmentId: string) {
  const attachment = session.attachments.find((candidate) => candidate.id === attachmentId)
  if (attachment == null) {
    throw new AttachmentNotFoundError(`attachment not found: ${attachmentId}`)
  }

  return attachment
}

function toTransportUnavailableError(error: unknown): TransportUnavailableError {
  if (error instanceof TransportUnavailableError) {
    return error
  }

  const message = error instanceof Error ? error.message : String(error)
  return new TransportUnavailableError(`transport unavailable: ${message}`)
}

async function callWorker<T>(operation: () => Promise<T>): Promise<T> {
  try {
    return await operation()
  } catch (error) {
    throw toTransportUnavailableError(error)
  }
}

export function createTransportService(deps: {
  sessionService: SessionService
  instanceService: InstanceService
  workerClient: ManagedInstanceWorkerClient
}) {
  const { sessionService, instanceService, workerClient } = deps

  async function getSessionAndAttachment(input: AttachmentTransportInput) {
    const session = await sessionService.getSession(input.sessionId)
    const attachment = getRequiredAttachment(session, input.attachmentId)

    return { attachment, session }
  }

  return {
    async ensureAttachmentTransport(input: AttachmentTransportInput): Promise<WorkerTransportSession> {
      const { attachment, session } = await getSessionAndAttachment(input)
      assertTransportableSession(session)

      if (attachment.status !== "connected") {
        throw new SessionInvalidRequestError(`attachment is not connected: ${attachment.id}`)
      }

      const instance = await instanceService.getManagedInstanceUnchecked(session.instanceId)
      const runtimeHandle = instance.runtimeHandle
      if (runtimeHandle == null) {
        throw new SessionInvalidRequestError(`instance is not running: ${instance.id}`)
      }

      return await callWorker(async () => await workerClient.createTransportSession({
        sessionId: session.id,
        attachmentId: attachment.id,
        instanceId: instance.id,
        runtimeClass: instance.runtimeClass,
        runtimeHandle,
        role: effectiveTransportRole(attachment),
      }))
    },

    async syncAttachmentRole(input: AttachmentTransportInput): Promise<void> {
      const { attachment, session } = await getSessionAndAttachment(input)
      const instance = await instanceService.getManagedInstanceUnchecked(session.instanceId)

      await callWorker(async () => await workerClient.updateTransportRole({
        instanceId: instance.id,
        attachmentId: attachment.id,
        role: effectiveTransportRole(attachment, input.role),
      }))
    },

    async destroyAttachmentTransport(input: AttachmentTransportInput): Promise<void> {
      const { attachment, session } = await getSessionAndAttachment(input)
      const instance = await instanceService.getManagedInstanceUnchecked(session.instanceId)

      await callWorker(async () => await workerClient.destroyTransportSession({
        instanceId: instance.id,
        attachmentId: attachment.id,
      }))
    },
  }
}
