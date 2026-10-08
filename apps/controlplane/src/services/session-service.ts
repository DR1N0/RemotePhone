import { randomUUID } from "node:crypto"

import type { SessionRole } from "../domain/types.js"
import type { AppDb } from "../persistence/db.js"
import {
  createAttachment,
  getAttachmentById,
  listAttachmentsForSession,
  setAttachmentPendingTransportRole,
  setAttachmentTransportTeardownPending,
  updateAttachmentRole,
  updateAttachmentStatus,
} from "../persistence/repositories/attachments.js"
import { getInstanceById } from "../persistence/repositories/instances.js"
import {
  clearOwnerAbsenceDeadline,
  closeSession as closeSessionRecord,
  createSession as createSessionRecord,
  getSessionById,
  listSessionsForInstance,
  setOwnerAbsenceDeadline,
} from "../persistence/repositories/sessions.js"
import { revokeAttachmentCredentials } from "../persistence/repositories/credentials.js"
import type { AttachmentRow, SessionRow } from "../persistence/schema.js"
import { attachmentTransportCoordinator } from "./attachment-transport-coordinator.js"
import { createAuditContext, createAuditService, type AuditContext, type AuditService } from "./audit-service.js"
import type { AuthenticatedUser, AuthService } from "./auth-service.js"

export class SessionServiceError extends Error {}
export class SessionConflictError extends SessionServiceError {}
export class SessionNotFoundError extends SessionServiceError {}
export class AttachmentNotFoundError extends SessionServiceError {}
export class SessionInvalidRequestError extends SessionServiceError {}
export class SessionForbiddenError extends SessionServiceError {}

export type SessionAttachmentRecord = {
  id: string
  sessionId: string
  clientId: string
  userId: string | null
  role: SessionRole
  restrictivePendingRole: AttachmentRow["restrictivePendingRole"] | null
  pendingTargetRole: AttachmentRow["pendingTargetRole"] | null
  requestedTransportRole: SessionRole
  effectiveTransportRole: SessionRole
  status: AttachmentRow["status"]
  transportTeardownPending: boolean
  createdAt: string
  updatedAt: string
}

export type SessionRecord = {
  id: string
  instanceId: string
  status: SessionRow["status"]
  ownerAbsenceDeadline: string | null
  attachments: SessionAttachmentRecord[]
  createdAt: string
  updatedAt: string
}

export type SessionService = ReturnType<typeof createSessionService>

const ATTACHMENT_CREDENTIAL_TTL_MS = 24 * 60 * 60_000

const roleOrder: Record<SessionRole, number> = {
  owner: 0,
  participant: 1,
  observer: 2,
}

function clampOwnerAbsenceTimeoutMs(value: number, min: number, max: number): number {
  return Math.min(Math.max(value, min), max)
}

function toAttachmentRecord(row: AttachmentRow): SessionAttachmentRecord {
  const restrictivePendingRole = row.restrictivePendingRole ?? null
  const pendingTargetRole = row.pendingTargetRole ?? null
  const requestedTransportRole = pendingTargetRole ?? row.role

  return {
    id: row.id,
    sessionId: row.sessionId,
    clientId: row.clientId,
    userId: row.userId ?? null,
    role: row.role,
    restrictivePendingRole,
    pendingTargetRole,
    requestedTransportRole,
    effectiveTransportRole: restrictivePendingRole != null || pendingTargetRole != null
      ? "observer"
      : requestedTransportRole,
    status: row.status,
    transportTeardownPending: row.transportTeardownPending,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function sortAttachments(rows: AttachmentRow[]): AttachmentRow[] {
  return [...rows].sort((left, right) => {
    const roleDelta = roleOrder[left.role] - roleOrder[right.role]
    if (roleDelta !== 0) return roleDelta

    const createdAtDelta = left.createdAt.localeCompare(right.createdAt)
    if (createdAtDelta !== 0) return createdAtDelta

    return left.updatedAt.localeCompare(right.updatedAt)
  })
}

function toSessionRecord(row: SessionRow, attachmentRows: AttachmentRow[]): SessionRecord {
  return {
    id: row.id,
    instanceId: row.instanceId,
    status: row.status,
    ownerAbsenceDeadline: row.ownerAbsenceDeadline ?? null,
    attachments: sortAttachments(attachmentRows).map(toAttachmentRecord),
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function getRequiredSession(db: AppDb, sessionId: string): SessionRow {
  const session = getSessionById(db, sessionId)
  if (session == null) throw new SessionNotFoundError(`session not found: ${sessionId}`)
  return session
}

function getRequiredAttachment(db: AppDb, sessionId: string, attachmentId: string): AttachmentRow {
  const attachment = getAttachmentById(db, attachmentId)
  if (attachment == null || attachment.sessionId !== sessionId) {
    throw new AttachmentNotFoundError(`attachment not found: ${attachmentId}`)
  }
  return attachment
}

function assertSessionMutable(session: SessionRow): void {
  if (session.status === "closed") throw new SessionInvalidRequestError(`session is closed: ${session.id}`)
}

export function createSessionService(deps: {
  db: AppDb
  authService: AuthService
  defaultOwnerAbsenceTimeoutMs: number
  ownerAbsenceTimeoutMinMs?: number
  ownerAbsenceTimeoutMaxMs?: number
  now?: () => Date
  destroyAttachmentTransport?: (
    input: { sessionId: string; attachmentId: string },
    audit: AuditContext,
  ) => Promise<void>
  /** Test seam for deterministic independent-connection interleavings. */
  beforeCreateSessionTransaction?: () => Promise<void>
  auditService?: AuditService
}) {
  const { db, authService, defaultOwnerAbsenceTimeoutMs } = deps
  const auditService = deps.auditService ?? createAuditService({ db })
  const ownerAbsenceTimeoutMinMs = deps.ownerAbsenceTimeoutMinMs ?? 30_000
  const ownerAbsenceTimeoutMaxMs = deps.ownerAbsenceTimeoutMaxMs ?? 3_600_000
  const now = deps.now ?? (() => new Date())

  function getSessionUnchecked(sessionId: string): SessionRecord {
    const session = getRequiredSession(db, sessionId)
    return toSessionRecord(session, listAttachmentsForSession(db, sessionId))
  }

  function assertAccountOwnsSession(session: SessionRow, caller: AuthenticatedUser): void {
    if (caller.scope !== "account") {
      throw new SessionForbiddenError("session moderation requires an account credential")
    }
    const instance = getInstanceById(db, session.instanceId)
    if (instance?.ownerId !== caller.userId) {
      throw new SessionForbiddenError(`caller is not the session owner: ${session.id}`)
    }
  }

  function assertAttachmentCaller(input: {
    sessionId: string
    attachmentId: string
    caller: AuthenticatedUser
  }): void {
    if (
      input.caller.scope !== "attachment"
      || input.caller.sessionId !== input.sessionId
      || input.caller.attachmentId !== input.attachmentId
    ) {
      throw new SessionForbiddenError("attachment credential is not bound to this session")
    }
  }

  return {
    async createSession(input: {
      instanceId: string
      ownerClientId: string
      caller: AuthenticatedUser
      ownerAbsenceTimeoutMs?: number
    }): Promise<{ session: SessionRecord; attachmentToken: string }> {
      if (input.caller.scope !== "account") {
        throw new SessionForbiddenError("session creation requires an account credential")
      }
      const ownerAbsenceTimeoutMs = input.ownerAbsenceTimeoutMs == null
        ? null
        : clampOwnerAbsenceTimeoutMs(input.ownerAbsenceTimeoutMs, ownerAbsenceTimeoutMinMs, ownerAbsenceTimeoutMaxMs)
      const sessionId = randomUUID()
      const attachmentId = randomUUID()

      await deps.beforeCreateSessionTransaction?.()
      const transaction = db.$client.transaction(() => {
        const instance = getInstanceById(db, input.instanceId)
        if (instance == null) throw new SessionNotFoundError(`instance not found: ${input.instanceId}`)
        if (instance.ownerId !== input.caller.userId) {
          throw new SessionForbiddenError(`caller does not own instance ${input.instanceId}`)
        }
        if (listSessionsForInstance(db, input.instanceId).some((session) => session.status !== "closed")) {
          throw new SessionConflictError(`active session already exists for instance ${input.instanceId}`)
        }
        createSessionRecord(db, {
          id: sessionId,
          instanceId: instance.id,
          status: "active",
          ownerAbsenceDeadline: null,
          ownerAbsenceTimeoutMs,
        })
        createAttachment(db, {
          id: attachmentId,
          sessionId,
          clientId: input.ownerClientId,
          userId: input.caller.userId,
          role: "owner",
          status: "connected",
        })
        const attachmentToken = authService.issueAttachmentCredential({
          userId: input.caller.userId,
          sessionId,
          attachmentId,
          expiresAt: new Date(now().getTime() + ATTACHMENT_CREDENTIAL_TTL_MS).toISOString(),
        })
        return { session: getSessionUnchecked(sessionId), attachmentToken }
      })
      return transaction.immediate()
    },

    async connectAttachment(input: { sessionId: string; attachmentId: string }): Promise<SessionAttachmentRecord> {
      assertSessionMutable(getRequiredSession(db, input.sessionId))
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
      if (attachment.status !== "connected") updateAttachmentStatus(db, { id: attachment.id, status: "connected" })
      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async setAttachmentPendingTransportRole(input: {
      sessionId: string
      attachmentId: string
      targetRole: "participant" | "observer" | null
    }): Promise<SessionAttachmentRecord> {
      getRequiredSession(db, input.sessionId)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
      setAttachmentPendingTransportRole(db, { id: attachment.id, pendingTargetRole: input.targetRole })
      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async promoteAttachment(input: {
      sessionId: string
      attachmentId: string
      caller: AuthenticatedUser
      requestId?: string
    }): Promise<SessionAttachmentRecord> {
      return db.$client.transaction(() => {
        const session = getRequiredSession(db, input.sessionId)
        assertAccountOwnsSession(session, input.caller)
        assertSessionMutable(session)
        const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
        if (attachment.role === "owner") throw new SessionInvalidRequestError(`owner attachment cannot change role: ${attachment.id}`)
        updateAttachmentRole(db, { id: attachment.id, role: "participant" })
        auditService.record({
          ...createAuditContext(input.caller.userId, input.caller.credentialId, input.requestId),
          action: "session.role.change",
          targetType: "attachment",
          targetId: attachment.id,
          outcome: "success",
        })
        return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
      })()
    },

    async demoteAttachment(input: {
      sessionId: string
      attachmentId: string
      caller: AuthenticatedUser
      requestId?: string
    }): Promise<SessionAttachmentRecord> {
      return db.$client.transaction(() => {
        const session = getRequiredSession(db, input.sessionId)
        assertAccountOwnsSession(session, input.caller)
        assertSessionMutable(session)
        const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
        if (attachment.role === "owner") throw new SessionInvalidRequestError(`owner attachment cannot be demoted: ${attachment.id}`)
        updateAttachmentRole(db, { id: attachment.id, role: "observer" })
        auditService.record({
          ...createAuditContext(input.caller.userId, input.caller.credentialId, input.requestId),
          action: "session.role.change",
          targetType: "attachment",
          targetId: attachment.id,
          outcome: "success",
        })
        return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
      })()
    },

    async disconnectAttachment(input: {
      sessionId: string
      attachmentId: string
      caller: AuthenticatedUser
      requestId?: string
    }): Promise<SessionRecord> {
      const transaction = db.$client.transaction(() => {
        const session = getRequiredSession(db, input.sessionId)
        assertSessionMutable(session)
        const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
        const kickedByAccount = input.caller.scope === "account"
        if (input.caller.scope === "attachment") {
          assertAttachmentCaller(input)
        } else {
          assertAccountOwnsSession(session, input.caller)
        }

        const wasAlreadyDisconnected = attachment.status === "disconnected"
        if (!wasAlreadyDisconnected) updateAttachmentStatus(db, { id: attachment.id, status: "disconnected" })
        if (kickedByAccount) {
          revokeAttachmentCredentials(db, {
            sessionId: input.sessionId,
            attachmentId: attachment.id,
            revokedAt: now().toISOString(),
          })
        }
        setAttachmentTransportTeardownPending(db, { id: attachment.id, pending: true })
        if (attachment.role === "owner" && !wasAlreadyDisconnected && session.ownerAbsenceDeadline == null) {
          const timeoutMs = session.ownerAbsenceTimeoutMs ?? defaultOwnerAbsenceTimeoutMs
          setOwnerAbsenceDeadline(db, {
            id: input.sessionId,
            ownerAbsenceDeadline: new Date(now().getTime() + timeoutMs).toISOString(),
          })
        }
        if (kickedByAccount) {
          auditService.record({
            ...createAuditContext(input.caller.userId, input.caller.credentialId, input.requestId),
            action: "session.attachment.disconnect",
            targetType: "attachment",
            targetId: attachment.id,
            outcome: "success",
          })
        }
        return getSessionUnchecked(input.sessionId)
      })
      return await attachmentTransportCoordinator.run(input, () => transaction.immediate())
    },

    async resumeAttachment(input: {
      sessionId: string
      attachmentId: string
      caller: AuthenticatedUser
    }): Promise<{ attachment: SessionAttachmentRecord; attachmentToken: string }> {
      const transaction = db.$client.transaction(() => {
        const session = getRequiredSession(db, input.sessionId)
        assertSessionMutable(session)
        assertAttachmentCaller(input)
        const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
        const attachmentToken = authService.rotateAttachmentCredential({
          credentialId: input.caller.credentialId,
          userId: input.caller.userId,
          sessionId: input.sessionId,
          attachmentId: input.attachmentId,
        })
        if (attachment.status !== "connected") updateAttachmentStatus(db, { id: attachment.id, status: "connected" })
        if (attachment.role === "owner") clearOwnerAbsenceDeadline(db, { id: input.sessionId })
        return { attachment: toAttachmentRecord(getRequiredAttachment(db, input.sessionId, attachment.id)), attachmentToken }
      })
      return await attachmentTransportCoordinator.run(input, () => transaction.immediate())
    },

    async refreshAttachmentCredential(input: {
      sessionId: string
      attachmentId: string
      caller: AuthenticatedUser
    }): Promise<{ attachmentToken: string }> {
      return db.$client.transaction(() => {
        assertSessionMutable(getRequiredSession(db, input.sessionId))
        assertAttachmentCaller(input)
        getRequiredAttachment(db, input.sessionId, input.attachmentId)
        return {
          attachmentToken: authService.rotateAttachmentCredential({
            credentialId: input.caller.credentialId,
            userId: input.caller.userId,
            sessionId: input.sessionId,
            attachmentId: input.attachmentId,
          }),
        }
      })()
    },

    async getSession(sessionId: string, caller: AuthenticatedUser): Promise<SessionRecord> {
      const session = getRequiredSession(db, sessionId)
      if (caller.scope === "attachment") {
        if (caller.sessionId !== sessionId || caller.attachmentId == null) {
          throw new SessionForbiddenError("attachment credential is not bound to this session")
        }
        getRequiredAttachment(db, sessionId, caller.attachmentId)
      } else {
        assertAccountOwnsSession(session, caller)
      }
      return getSessionUnchecked(sessionId)
    },

    async getSessionUnchecked(sessionId: string): Promise<SessionRecord> {
      return getSessionUnchecked(sessionId)
    },

    async authorizeSessionModeration(sessionId: string, caller: AuthenticatedUser): Promise<void> {
      assertAccountOwnsSession(getRequiredSession(db, sessionId), caller)
    },

    async completeAttachmentTransportTeardown(input: {
      sessionId: string
      attachmentId: string
    }): Promise<void> {
      getRequiredAttachment(db, input.sessionId, input.attachmentId)
      setAttachmentTransportTeardownPending(db, { id: input.attachmentId, pending: false })
    },

    async closeSession(sessionId: string, providedAudit?: AuditContext): Promise<SessionRecord> {
      const audit = providedAudit ?? createAuditContext(null, null)
      const transaction = db.$client.transaction(() => {
        getRequiredSession(db, sessionId)
        const attachments = listAttachmentsForSession(db, sessionId)
        for (const attachment of attachments) {
          if (attachment.status !== "disconnected") {
            updateAttachmentStatus(db, { id: attachment.id, status: "disconnected" })
          }
          revokeAttachmentCredentials(db, {
            sessionId,
            attachmentId: attachment.id,
            revokedAt: now().toISOString(),
          })
          setAttachmentTransportTeardownPending(db, { id: attachment.id, pending: true })
        }
        closeSessionRecord(db, { id: sessionId })
        auditService.record({
          ...audit,
          action: "session.close",
          targetType: "session",
          targetId: sessionId,
          outcome: "success",
        })
        return { attachments, session: getSessionUnchecked(sessionId) }
      })
      const teardownTargets = listAttachmentsForSession(db, sessionId)
        .map((attachment) => ({ sessionId, attachmentId: attachment.id }))
      const closed = await attachmentTransportCoordinator.runMany(teardownTargets, () => transaction.immediate())
      if (deps.destroyAttachmentTransport != null) {
        for (const attachment of closed.attachments) {
          try {
            await deps.destroyAttachmentTransport({ sessionId, attachmentId: attachment.id }, audit)
            setAttachmentTransportTeardownPending(db, { id: attachment.id, pending: false })
          } catch {
            // Durable revocation remains pending for reconciliation.
          }
        }
      }
      return closed.session
    },
  }
}
