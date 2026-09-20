import { randomUUID } from "node:crypto"

import type { SessionRole } from "../domain/types.js"
import type { AppDb } from "../persistence/db.js"
import {
  createAttachment,
  getAttachmentById,
  getAttachmentByReconnectToken,
  listAttachmentsForSession,
  setAttachmentRestrictivePendingRole,
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

export type SessionAttachmentRecord = {
  id: string
  sessionId: string
  clientId: string
  role: SessionRole
  restrictivePendingRole: AttachmentRow["restrictivePendingRole"] | null
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
  return {
    id: row.id,
    sessionId: row.sessionId,
    clientId: row.clientId,
    role: row.role,
    restrictivePendingRole: row.restrictivePendingRole ?? null,
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
    }): Promise<SessionRecord> {
      const instance = getInstanceById(db, input.instanceId)
      if (instance == null) {
        throw new SessionNotFoundError(`instance not found: ${input.instanceId}`)
      }

      const existingSession = listSessionsForInstance(db, input.instanceId)
        .find((session) => session.status !== "closed")
      if (existingSession != null) {
        throw new SessionConflictError(`active session already exists for instance ${input.instanceId}`)
      }

      const sessionId = randomUUID()
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
        role: "owner",
        status: "connected",
        reconnectToken: randomUUID(),
      })

      return await this.getSession(sessionId)
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

    async setAttachmentRestrictivePendingRole(input: {
      sessionId: string
      attachmentId: string
      role: "observer" | null
    }): Promise<SessionAttachmentRecord> {
      getRequiredSession(db, input.sessionId)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)
      setAttachmentRestrictivePendingRole(db, {
        id: attachment.id,
        restrictivePendingRole: input.role,
      })

      return toAttachmentRecord(getRequiredAttachment(db, input.sessionId, input.attachmentId))
    },

    async promoteAttachment(input: {
      sessionId: string
      attachmentId: string
    }): Promise<SessionAttachmentRecord> {
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
    }): Promise<SessionAttachmentRecord> {
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
    }): Promise<SessionRecord> {
      const session = getRequiredSession(db, input.sessionId)
      assertSessionMutable(session)
      const attachment = getRequiredAttachment(db, input.sessionId, input.attachmentId)

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
      return toSessionRecord(session, attachments)
    },

    async closeSession(sessionId: string): Promise<SessionRecord> {
      getRequiredSession(db, sessionId)
      closeSessionRecord(db, { id: sessionId })
      return await this.getSession(sessionId)
    },
  }
}
