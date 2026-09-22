import { randomUUID } from "node:crypto"

import type { SessionRole } from "../domain/types.js"
import type { AppDb } from "../persistence/db.js"
import {
  createAttachment,
  getAttachmentById,
  getAttachmentByReconnectToken,
  listAttachmentsForSession,
  setAttachmentPendingTransportRole,
  updateAttachmentRole,
  updateAttachmentStatus,
} from "../persistence/repositories/attachments.js"
import { getInstanceById } from "../persistence/repositories/instances.js"
import { activateSession, closeSession as closeSessionRecord, createSession as createSessionRecord, getSessionById, listSessionsForInstance, markSessionIdle as markSessionIdleRecord } from "../persistence/repositories/sessions.js"
import type { AttachmentRow, SessionRow } from "../persistence/schema.js"

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
  principalId: string | null
  role: SessionRole
  restrictivePendingRole: AttachmentRow["restrictivePendingRole"] | null
  pendingTargetRole: AttachmentRow["pendingTargetRole"] | null
  requestedTransportRole: SessionRole
  effectiveTransportRole: SessionRole
  status: AttachmentRow["status"]
  reconnectToken: string | null
  createdAt: string
  updatedAt: string
}

export type SessionRecord = {
  id: string
  instanceId: string
  status: SessionRow["status"]
  idleExpiresAt: string | null
  reconnectToken: string | null
  attachments: SessionAttachmentRecord[]
  createdAt: string
  updatedAt: string
}

export type SessionService = ReturnType<typeof createSessionService>

const roleOrder: Record<SessionRole, number> = {
  owner: 0,
  participant: 1,
  observer: 2,
}

function toAttachmentRecord(row: AttachmentRow): SessionAttachmentRecord {
  const restrictivePendingRole = row.restrictivePendingRole ?? null
  const pendingTargetRole = row.pendingTargetRole ?? null
  const requestedTransportRole = pendingTargetRole ?? row.role

  return {
    id: row.id,
    sessionId: row.sessionId,
    clientId: row.clientId,
    principalId: row.principalId ?? null,
    role: row.role,
    restrictivePendingRole,
    pendingTargetRole,
    requestedTransportRole,
    effectiveTransportRole: restrictivePendingRole != null || pendingTargetRole != null
      ? "observer"
      : requestedTransportRole,
    status: row.status,
    reconnectToken: row.reconnectToken ?? null,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function sortAttachments(rows: AttachmentRow[]): AttachmentRow[] {
  return [...rows].sort((left, right) => {
    const roleDelta = roleOrder[left.role] - roleOrder[right.role]
    if (roleDelta !== 0) {
      return roleDelta
    }

    const createdAtDelta = left.createdAt.localeCompare(right.createdAt)
    if (createdAtDelta !== 0) {
      return createdAtDelta
    }

    return left.updatedAt.localeCompare(right.updatedAt)
  })
}

function toSessionRecord(row: SessionRow, attachmentRows: AttachmentRow[]): SessionRecord {
  return {
    id: row.id,
    instanceId: row.instanceId,
    status: row.status,
    idleExpiresAt: row.idleExpiresAt ?? null,
    reconnectToken: row.reconnectToken ?? null,
    attachments: sortAttachments(attachmentRows).map(toAttachmentRecord),
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function getRequiredSession(db: AppDb, sessionId: string): SessionRow {
  const session = getSessionById(db, sessionId)
  if (session == null) {
    throw new SessionNotFoundError(`session not found: ${sessionId}`)
  }

  return session
}

function getRequiredAttachment(db: AppDb, sessionId: string, attachmentId: string): AttachmentRow {
  const attachment = getAttachmentById(db, attachmentId)
  if (attachment == null || attachment.sessionId !== sessionId) {
    throw new AttachmentNotFoundError(`attachment not found: ${attachmentId}`)
  }

  return attachment
}

function getRequiredAttachmentByReconnectToken(
  db: AppDb,
  sessionId: string,
  reconnectToken: string,
): AttachmentRow {
  const attachment = getAttachmentByReconnectToken(db, reconnectToken)
  if (attachment == null || attachment.sessionId !== sessionId) {
    throw new AttachmentNotFoundError(`attachment not found for reconnect token`)
  }

  return attachment
}

function assertSessionMutable(session: SessionRow): void {
  if (session.status === "closed") {
    throw new SessionInvalidRequestError(`session is closed: ${session.id}`)
  }
}

function assertIsSessionOwner(db: AppDb, sessionId: string, requesterPrincipalId: string): void {
  const ownerAttachment = listAttachmentsForSession(db, sessionId).find((attachment) => attachment.role === "owner")
  if (ownerAttachment?.principalId !== requesterPrincipalId) {
    throw new SessionForbiddenError(
      `principal ${requesterPrincipalId} is not the owner of session ${sessionId}`,
    )
  }
}

export function createSessionService(deps: {
  db: AppDb
  idleTtlMs: number
  now?: () => Date
}) {
  const { db, idleTtlMs } = deps
  const now = deps.now ?? (() => new Date())

  return {
    async createSession(input: {
      instanceId: string
      ownerClientId: string
      requesterPrincipalId: string
    }): Promise<SessionRecord> {
      const instance = getInstanceById(db, input.instanceId)
      if (instance == null) {
        throw new SessionNotFoundError(`instance not found: ${input.instanceId}`)
      }

      if (instance.ownerId !== input.requesterPrincipalId) {
        throw new SessionForbiddenError(
          `principal ${input.requesterPrincipalId} does not own instance ${input.instanceId}`,
        )
      }

      const existingSession = listSessionsForInstance(db, input.instanceId)
        .find((session) => session.status !== "closed")
      if (existingSession != null) {
        throw new SessionConflictError(`active session already exists for instance ${input.instanceId}`)
      }

      const sessionId = randomUUID()
      return db.$client.transaction(() => {
        createSessionRecord(db, {
          id: sessionId,
          instanceId: instance.id,
          status: "active",
          idleExpiresAt: null,
          reconnectToken: randomUUID(),
        })
        createAttachment(db, {
          id: randomUUID(),
          sessionId,
          clientId: input.ownerClientId,
          principalId: input.requesterPrincipalId,
          role: "owner",
          status: "connected",
          reconnectToken: randomUUID(),
        })

        return toSessionRecord(
          getRequiredSession(db, sessionId),
          listAttachmentsForSession(db, sessionId),
        )
      })()
    },

    async joinSession(input: {
      sessionId: string
      clientId: string
    }): Promise<SessionAttachmentRecord> {
      const session = getRequiredSession(db, input.sessionId)
      if (session.status === "closed") {
        throw new SessionInvalidRequestError(`session is closed: ${input.sessionId}`)
      }

      if (session.status === "idle") {
        activateSession(db, { id: input.sessionId })
      }

      const attachmentId = randomUUID()
      createAttachment(db, {
        id: attachmentId,
        sessionId: input.sessionId,
        clientId: input.clientId,
        role: "observer",
        status: "connected",
        reconnectToken: randomUUID(),
      })

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, attachmentId))
    },

    async connectAttachment(input: {
      sessionId: string
      attachmentId: string
    }): Promise<SessionAttachmentRecord> {
      const session = getRequiredSession(db, input.sessionId)
      assertSessionMutable(session)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)

      if (attachment.status !== "connected") {
        updateAttachmentStatus(db, {
          id: attachment.id,
          status: "connected",
        })
      }

      if (session.status === "idle") {
        activateSession(db, { id: input.sessionId })
      }

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async setAttachmentPendingTransportRole(input: {
      sessionId: string
      attachmentId: string
      targetRole: "participant" | "observer" | null
    }): Promise<SessionAttachmentRecord> {
      getRequiredSession(db, input.sessionId)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
      setAttachmentPendingTransportRole(db, {
        id: attachment.id,
        pendingTargetRole: input.targetRole,
      })

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async promoteAttachment(input: {
      sessionId: string
      attachmentId: string
      requesterPrincipalId: string
    }): Promise<SessionAttachmentRecord> {
      assertIsSessionOwner(db, input.sessionId, input.requesterPrincipalId)
      const session = getRequiredSession(db, input.sessionId)
      assertSessionMutable(session)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)

      if (attachment.role === "owner") {
        throw new SessionInvalidRequestError(`owner attachment cannot change role: ${attachment.id}`)
      }

      updateAttachmentRole(db, {
        id: input.attachmentId,
        role: "participant",
      })

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async demoteAttachment(input: {
      sessionId: string
      attachmentId: string
      requesterPrincipalId: string
    }): Promise<SessionAttachmentRecord> {
      assertIsSessionOwner(db, input.sessionId, input.requesterPrincipalId)
      const session = getRequiredSession(db, input.sessionId)
      assertSessionMutable(session)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)

      if (attachment.role === "owner") {
        throw new SessionInvalidRequestError(`owner attachment cannot be demoted: ${attachment.id}`)
      }

      updateAttachmentRole(db, {
        id: input.attachmentId,
        role: "observer",
      })

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async disconnectAttachment(input: {
      sessionId: string
      attachmentId: string
      requesterPrincipalId?: string
      requesterReconnectToken?: string
    }): Promise<SessionRecord> {
      const session = getRequiredSession(db, input.sessionId)
      assertSessionMutable(session)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)

      const selfAuthorized = input.requesterReconnectToken != null
        && input.requesterReconnectToken === attachment.reconnectToken
      const ownerAuthorized = input.requesterPrincipalId != null
        && listAttachmentsForSession(db, input.sessionId).find((a) => a.role === "owner")?.principalId
          === input.requesterPrincipalId
      if (!selfAuthorized && !ownerAuthorized) {
        throw new SessionForbiddenError(
          `caller may not disconnect attachment ${input.attachmentId} in session ${input.sessionId}`,
        )
      }

      const wasAlreadyDisconnected = attachment.status === "disconnected"
      if (!wasAlreadyDisconnected) {
        updateAttachmentStatus(db, {
          id: input.attachmentId,
          status: "disconnected",
        })
      }

      const attachments = listAttachmentsForSession(db, input.sessionId)
      const hasConnectedAttachments = attachments.some((attachment) => attachment.status === "connected")

      if (!hasConnectedAttachments && session.status !== "idle") {
        markSessionIdleRecord(db, {
          id: input.sessionId,
          idleExpiresAt: new Date(now().getTime() + idleTtlMs).toISOString(),
        })
      }

      return await this.getSession(input.sessionId)
    },

    async resumeAttachment(input: {
      sessionId: string
      reconnectToken: string
    }): Promise<SessionAttachmentRecord> {
      const session = getRequiredSession(db, input.sessionId)
      if (session.status === "closed") {
        throw new SessionInvalidRequestError(`session is closed: ${input.sessionId}`)
      }

      const attachment = getRequiredAttachmentByReconnectToken(db, input.sessionId, input.reconnectToken)
      updateAttachmentStatus(db, {
        id: attachment.id,
        status: "connected",
      })

      if (session.status === "idle") {
        activateSession(db, { id: input.sessionId })
      }

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, attachment.id))
    },

    async getSession(sessionId: string): Promise<SessionRecord> {
      const session = getRequiredSession(db, sessionId)
      const attachments = listAttachmentsForSession(db, sessionId)
      const record = toSessionRecord(session, attachments)
      return {
        ...record,
        attachments: record.attachments.map((a) => ({ ...a, reconnectToken: null })),
      }
    },

    async closeSession(sessionId: string): Promise<SessionRecord> {
      getRequiredSession(db, sessionId)
      closeSessionRecord(db, { id: sessionId })
      return await this.getSession(sessionId)
    },
  }
}
