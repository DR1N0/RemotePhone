import { randomUUID } from "node:crypto"

import {
  createAttachment,
  getAttachmentById,
  listAttachmentsForSession,
} from "../persistence/repositories/attachments.js"
import {
  consumeSessionInvite,
  createSessionInvite as createSessionInviteRow,
  getSessionInviteByTokenHash,
} from "../persistence/repositories/session-invites.js"
import { getSessionById } from "../persistence/repositories/sessions.js"
import { createUser, getUserById, toUserDto } from "../persistence/repositories/users.js"
import type { AppDb } from "../persistence/db.js"
import type { AttachmentRow, SessionInviteRow, UserDto } from "../persistence/schema.js"
import type { AuthService } from "./auth-service.js"
import {
  SessionForbiddenError,
  SessionInvalidRequestError,
  SessionNotFoundError,
  type SessionAttachmentRecord,
} from "./session-service.js"
import { createAuditContext, createAuditService, type AuditService } from "./audit-service.js"
import { generateToken, hashToken } from "./token.js"

const DEFAULT_INVITE_TTL_MS = 10 * 60_000
const MIN_INVITE_TTL_MS = 60_000
const MAX_INVITE_TTL_MS = 60 * 60_000
const ATTACHMENT_CREDENTIAL_TTL_MS = 24 * 60 * 60_000

export class SessionInviteInvalidError extends Error {}

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

export type SessionInviteService = ReturnType<typeof createSessionInviteService>

export function createSessionInviteService(deps: {
  db: AppDb
  authService: AuthService
  auditService?: AuditService
  now?: () => Date
  /** Test seam for deterministic independent-connection interleavings. */
  beforeCreateSessionInviteTransaction?: () => Promise<void>
}) {
  const { db, authService } = deps
  const auditService = deps.auditService ?? createAuditService({ db })
  const now = deps.now ?? (() => new Date())

  function consumeValidSessionInvite(
    database: AppDb,
    input: { tokenHash: string; now: string },
  ): SessionInviteRow | null {
    const invite = getSessionInviteByTokenHash(database, input.tokenHash)
    if (invite == null || !consumeSessionInvite(database, {
      id: invite.id,
      consumedAt: input.now,
      now: input.now,
    })) {
      return null
    }
    return invite
  }

  function validateGuestDisplayName(displayName: string | undefined): string {
    if (displayName == null) return "Guest"
    if (displayName.length < 1 || displayName.length > 40 || displayName.trim().length === 0) {
      throw new SessionInviteInvalidError("guest displayName must be 1 to 40 non-whitespace characters")
    }
    return displayName
  }

  function createGuestUser(database: AppDb, displayName: string): string {
    const userId = randomUUID()
    createUser(database, {
      id: userId,
      displayName,
      userType: "guest",
      username: null,
      passwordHash: null,
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    return userId
  }

  function createObserverAttachment(
    database: AppDb,
    input: { sessionId: string; userId: string },
  ): AttachmentRow {
    const attachmentId = randomUUID()
    createAttachment(database, {
      id: attachmentId,
      sessionId: input.sessionId,
      userId: input.userId,
      clientId: `invite-${attachmentId}`,
      role: "observer",
      status: "connected",
    })
    const attachment = getAttachmentById(database, attachmentId)
    if (attachment == null) {
      throw new Error("redeemed invite attachment was not persisted")
    }
    return attachment
  }

  function requireRegisteredUser(userId: string | undefined): string {
    if (userId == null) {
      throw new SessionInviteInvalidError("registered redemption requires an account user")
    }
    const user = getUserById(db, userId)
    if (user == null || user.status !== "active" || user.userType === "guest") {
      throw new SessionInviteInvalidError("registered redemption requires an active registered user")
    }
    return user.id
  }

  return {
    async createSessionInvite(input: {
      sessionId: string
      actorUserId: string
      actorCredentialId: string | null
      requestId: string
      ttlMs?: number
    }): Promise<{ token: string; expiresAt: string }> {
      const ttlMs = input.ttlMs ?? DEFAULT_INVITE_TTL_MS
      if (!Number.isInteger(ttlMs) || ttlMs < MIN_INVITE_TTL_MS || ttlMs > MAX_INVITE_TTL_MS) {
        throw new SessionInvalidRequestError("session invite TTL must be between 1 and 60 minutes")
      }

      const token = generateToken()
      const expiresAt = new Date(now().getTime() + ttlMs).toISOString()
      const inviteId = randomUUID()
      await deps.beforeCreateSessionInviteTransaction?.()
      const transaction = db.$client.transaction(() => {
        const session = getSessionById(db, input.sessionId)
        if (session == null) {
          throw new SessionNotFoundError(`session not found: ${input.sessionId}`)
        }
        if (session.status !== "active") {
          throw new SessionInvalidRequestError(`session is closed: ${input.sessionId}`)
        }
        const owner = listAttachmentsForSession(db, input.sessionId)
          .find((attachment) => attachment.role === "owner")
        if (owner?.userId !== input.actorUserId) {
          throw new SessionForbiddenError(`user ${input.actorUserId} is not the owner of session ${input.sessionId}`)
        }
        createSessionInviteRow(db, {
          id: inviteId,
          sessionId: input.sessionId,
          createdByUserId: input.actorUserId,
          tokenHash: hashToken(token),
          expiresAt,
          consumedAt: null,
        })
        auditService.record({
          ...createAuditContext(input.actorUserId, input.actorCredentialId, input.requestId),
          action: "session.invite.issue",
          targetType: "session_invite",
          targetId: inviteId,
          outcome: "success",
        })
        return { token, expiresAt }
      })
      return transaction.immediate()
    },

    async redeemSessionInvite(input: {
      token: string
      mode: "registered" | "guest"
      requesterUserId?: string
      actorCredentialId: string | null
      requestId: string
      displayName?: string
    }): Promise<{ attachment: SessionAttachmentRecord; attachmentToken: string; user: UserDto }> {
      const guestDisplayName = input.mode === "guest" ? validateGuestDisplayName(input.displayName) : undefined
      const transaction = db.$client.transaction(() => {
        const redeemedAt = now()
        const invite = consumeValidSessionInvite(db, {
          tokenHash: hashToken(input.token),
          now: redeemedAt.toISOString(),
        })
        if (invite == null) {
          throw new SessionInviteInvalidError("invite is invalid or expired")
        }

        const session = getSessionById(db, invite.sessionId)
        if (session == null || session.status !== "active") {
          throw new SessionInviteInvalidError("invite session is not active")
        }

        const userId = input.mode === "guest"
          ? createGuestUser(db, guestDisplayName!)
          : requireRegisteredUser(input.requesterUserId)
        const attachment = createObserverAttachment(db, { sessionId: invite.sessionId, userId })
        const attachmentToken = authService.issueAttachmentCredential({
          userId,
          sessionId: invite.sessionId,
          attachmentId: attachment.id,
          expiresAt: new Date(redeemedAt.getTime() + ATTACHMENT_CREDENTIAL_TTL_MS).toISOString(),
        })
        const user = getUserById(db, userId)
        if (user == null) {
          throw new Error("redeemed invite user was not persisted")
        }
        auditService.record({
          ...createAuditContext(userId, input.actorCredentialId, input.requestId),
          action: "session.invite.redeem",
          targetType: "session_invite",
          targetId: invite.id,
          outcome: "success",
        })
        return { attachment: toAttachmentRecord(attachment), attachmentToken, user: toUserDto(user) }
      })
      return transaction.immediate()
    },
  }
}
