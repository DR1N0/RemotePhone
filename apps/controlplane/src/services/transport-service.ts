import type { SessionRole } from "../domain/types.js"
import { attachmentTransportCoordinator } from "./attachment-transport-coordinator.js"
import { createAuditContext, type AuditContext, type AuditService } from "./audit-service.js"
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
  audit?: AuditContext
}

type AuditedAttachmentTransportInput = AttachmentTransportInput & {
  audit: AuditContext
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
  if (session.status !== "active") {
    throw new SessionInvalidRequestError(`session is not transportable: ${session.id}`)
  }
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
  auditService?: AuditService
}) {
  const { sessionService, instanceService, workerClient } = deps
  const { auditService } = deps

  async function getSessionAndAttachment(input: AttachmentTransportInput) {
    const session = await sessionService.getSessionUnchecked(input.sessionId)
    const attachment = getRequiredAttachment(session, input.attachmentId)

    return { attachment, session }
  }

  async function destroyAttachmentTransportUnlocked(input: AttachmentTransportInput): Promise<void> {
    const { attachment, session } = await getSessionAndAttachment(input)
    const instance = await instanceService.getManagedInstanceUnchecked(session.instanceId)
    const audit = input.audit ?? createAuditContext(null, null)
    auditService?.record({
      ...audit,
      action: "transport.teardown",
      targetType: "attachment",
      targetId: attachment.id,
      outcome: "attempt",
    })
    try {
      await callWorker(async () => await workerClient.destroyTransportSession({
        instanceId: instance.id,
        attachmentId: attachment.id,
      }))
    } catch (error) {
      auditService?.record({
        ...audit,
        action: "transport.teardown",
        targetType: "attachment",
        targetId: attachment.id,
        outcome: "failure",
      })
      throw error
    }
    auditService?.record({
      ...audit,
      action: "transport.teardown",
      targetType: "attachment",
      targetId: attachment.id,
      outcome: "success",
    })
  }

  return {
    async ensureAttachmentTransport(input: AuditedAttachmentTransportInput): Promise<WorkerTransportSession> {
      return await attachmentTransportCoordinator.run(input, async () => {
        const { attachment, session } = await getSessionAndAttachment(input)
        assertTransportableSession(session)

        if (attachment.status !== "connected") {
          throw new SessionInvalidRequestError(`attachment is not connected: ${attachment.id}`)
        }
        if (attachment.transportTeardownPending) {
          await destroyAttachmentTransportUnlocked(input)
          await sessionService.completeAttachmentTransportTeardown(input)
        }

        const instance = await instanceService.getManagedInstanceUnchecked(session.instanceId)
        const runtimeHandle = instance.runtimeHandle
        if (runtimeHandle == null) {
          throw new SessionInvalidRequestError(`instance is not running: ${instance.id}`)
        }

        auditService?.record({
          ...input.audit,
          action: "transport.create",
          targetType: "attachment",
          targetId: attachment.id,
          outcome: "attempt",
        })
        try {
          const transport = await callWorker(async () => await workerClient.createTransportSession({
            sessionId: session.id,
            attachmentId: attachment.id,
            instanceId: instance.id,
            runtimeClass: instance.runtimeClass,
            runtimeHandle,
            role: effectiveTransportRole(attachment),
          }))
          auditService?.record({
            ...input.audit,
            action: "transport.create",
            targetType: "attachment",
            targetId: attachment.id,
            outcome: "success",
          })
          return transport
        } catch (error) {
          auditService?.record({
            ...input.audit,
            action: "transport.create",
            targetType: "attachment",
            targetId: attachment.id,
            outcome: "failure",
          })
          throw error
        }
      })
    },

    async syncAttachmentRole(input: AuditedAttachmentTransportInput): Promise<void> {
      await attachmentTransportCoordinator.run(input, async () => {
        const { attachment, session } = await getSessionAndAttachment(input)
        const instance = await instanceService.getManagedInstanceUnchecked(session.instanceId)
        auditService?.record({
          ...input.audit,
          action: "transport.role.update",
          targetType: "attachment",
          targetId: attachment.id,
          outcome: "attempt",
        })
        try {
          await callWorker(async () => await workerClient.updateTransportRole({
            instanceId: instance.id,
            attachmentId: attachment.id,
            role: effectiveTransportRole(attachment, input.role),
          }))
          auditService?.record({
            ...input.audit,
            action: "transport.role.update",
            targetType: "attachment",
            targetId: attachment.id,
            outcome: "success",
          })
        } catch (error) {
          auditService?.record({
            ...input.audit,
            action: "transport.role.update",
            targetType: "attachment",
            targetId: attachment.id,
            outcome: "failure",
          })
          throw error
        }
      })
    },

    async destroyAttachmentTransport(input: AttachmentTransportInput): Promise<void> {
      await attachmentTransportCoordinator.run(input, async () => {
        await destroyAttachmentTransportUnlocked(input)
      })
    },

    async teardownPendingAttachment(input: AttachmentTransportInput): Promise<boolean> {
      return await attachmentTransportCoordinator.run(input, async () => {
        const { attachment } = await getSessionAndAttachment(input)
        if (!attachment.transportTeardownPending) return false
        await destroyAttachmentTransportUnlocked(input)
        await sessionService.completeAttachmentTransportTeardown(input)
        return true
      })
    },
  }
}
