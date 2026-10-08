import type { AppDb } from "../persistence/db.js"
import {
  listAttachmentsForSession,
  listPendingTransportTeardownAttachments,
  setAttachmentTransportTeardownPending,
  updateAttachmentStatus,
} from "../persistence/repositories/attachments.js"
import { revokeAttachmentCredentials } from "../persistence/repositories/credentials.js"
import { listInstances, updateInstanceRuntime } from "../persistence/repositories/instances.js"
import { closeSession, getSessionById, listSessionsWithExpiredOwnerAbsence } from "../persistence/repositories/sessions.js"
import type { InstanceRow } from "../persistence/schema.js"
import { attachmentTransportCoordinator } from "./attachment-transport-coordinator.js"
import { createAuditContext, createAuditService, type AuditService } from "./audit-service.js"
import type { WorkerInstanceResponse } from "./worker-client.js"

export async function runReconcilePass(deps: {
  db: AppDb
  now: Date
  inspectRunningInstance: (instanceId: string) => Promise<WorkerInstanceResponse>
  teardownPendingAttachment?: (input: { sessionId: string; attachmentId: string }) => Promise<boolean>
  /** Test seam used to model a different SQLite connection resuming after selection. */
  beforeExpiredSessionTransaction?: (sessionId: string) => Promise<void>
  auditService?: AuditService
}): Promise<void> {
  const { db, now, inspectRunningInstance } = deps
  const auditService = deps.auditService ?? createAuditService({ db })
  const nowIso = now.toISOString()

  const expiredOwnerAbsenceSessions = listSessionsWithExpiredOwnerAbsence(db, nowIso)

  for (const selectedSession of expiredOwnerAbsenceSessions) {
    await deps.beforeExpiredSessionTransaction?.(selectedSession.id)
    // Keys are only used to serialize post-commit transport work. Eligibility is re-read
    // under SQLite's immediate write lock, because another process may have resumed first.
    const teardownTargets = listAttachmentsForSession(db, selectedSession.id)
      .map((attachment) => ({ sessionId: selectedSession.id, attachmentId: attachment.id }))
    const transaction = db.$client.transaction(() => {
      const session = getSessionById(db, selectedSession.id)
      if (
        session == null
        || session.status !== "active"
        || session.ownerAbsenceDeadline == null
        || session.ownerAbsenceDeadline > nowIso
      ) {
        return false
      }
      const attachments = listAttachmentsForSession(db, session.id)
      for (const attachment of attachments) {
        if (attachment.status === "connected") {
          updateAttachmentStatus(db, { id: attachment.id, status: "disconnected" })
        }
        revokeAttachmentCredentials(db, {
          sessionId: session.id,
          attachmentId: attachment.id,
          revokedAt: nowIso,
        })
        setAttachmentTransportTeardownPending(db, { id: attachment.id, pending: true })
      }
      closeSession(db, { id: session.id })
      auditService.record({
        ...createAuditContext(null, null),
        action: "session.close",
        targetType: "session",
        targetId: session.id,
        outcome: "success",
      })
      return true
    })
    const closed = await attachmentTransportCoordinator.runMany(teardownTargets, () => transaction.immediate())
    if (!closed) continue
  }

  if (deps.teardownPendingAttachment != null) {
    for (const attachment of listPendingTransportTeardownAttachments(db)) {
      try {
        await deps.teardownPendingAttachment({
          sessionId: attachment.sessionId,
          attachmentId: attachment.id,
        })
      } catch {
        // Revocation is already durable; retain pending work for the next pass.
      }
    }
  }

  const runningInstances = listInstances(db).filter((instance) => instance.desiredState === "running")
  for (const instance of runningInstances) {
    const inspection = await inspectRunningInstance(instance.id)
    updateInstanceRuntime(db, {
      id: instance.id,
      observedState: inspection.state as InstanceRow["observedState"],
      runtimeHandle: inspection.handle,
      lastError: inspection.lastError ?? null,
    })
  }
}
