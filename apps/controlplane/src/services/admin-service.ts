import type { AppDb } from "../persistence/db.js"
import {
  getAttachmentById,
  listAttachmentsForSession,
  listAttachmentsForUser,
  setAttachmentTransportTeardownPending,
  updateAttachmentStatus,
} from "../persistence/repositories/attachments.js"
import {
  getAccountCredentialForUser,
  listAccountCredentialsForUser,
  revokeAttachmentCredentials,
  revokeCredential,
} from "../persistence/repositories/credentials.js"
import { listInstances } from "../persistence/repositories/instances.js"
import { closeSession } from "../persistence/repositories/sessions.js"
import { setUserStatus } from "../persistence/repositories/users.js"
import {
  countActiveAdmins,
  getUserById,
  listAccountUsers,
  toUserDto,
  updateUserType,
} from "../persistence/repositories/users.js"
import type { CredentialRow, InstanceRow, UserStatus, UserRow, UserType } from "../persistence/schema.js"
import { attachmentTransportCoordinator } from "./attachment-transport-coordinator.js"
import { createAuditContext, createAuditService, type AuditContext, type AuditService } from "./audit-service.js"

export class AdminServiceError extends Error {}
export class NotAdminError extends AdminServiceError {}
export class UserNotFoundError extends AdminServiceError {}
export class LastActiveAdminError extends AdminServiceError {}
export class GuestAttachmentNotFoundError extends AdminServiceError {}
export class AccountCredentialNotFoundError extends AdminServiceError {}

export type AdminCredentialDto = Pick<CredentialRow,
  "id" | "deviceLabel" | "scope" | "notBefore" | "expiresAt" | "revokedAt" | "createdAt" | "updatedAt"
>

export type AdminInstanceDto = Pick<InstanceRow,
  "id" | "ownerId" | "name" | "runtimeClass" | "templateId" | "desiredState" | "observedState" | "lastError" | "createdAt" | "updatedAt"
>

function toAdminCredentialDto(row: CredentialRow): AdminCredentialDto {
  return {
    id: row.id,
    deviceLabel: row.deviceLabel,
    scope: row.scope,
    notBefore: row.notBefore,
    expiresAt: row.expiresAt,
    revokedAt: row.revokedAt,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function toAdminInstanceDto(row: InstanceRow): AdminInstanceDto {
  return {
    id: row.id,
    ownerId: row.ownerId,
    name: row.name,
    runtimeClass: row.runtimeClass,
    templateId: row.templateId,
    desiredState: row.desiredState,
    observedState: row.observedState,
    lastError: row.lastError,
    createdAt: row.createdAt,
    updatedAt: row.updatedAt,
  }
}

function requireActiveAdmin(db: AppDb, userId: string): UserRow {
  const user = getUserById(db, userId)
  if (user == null) {
    throw new UserNotFoundError(`user not found: ${userId}`)
  }
  if (user.userType !== "admin" || user.status !== "active") {
    throw new NotAdminError(`user is not an active admin: ${userId}`)
  }
  return user
}

function requireTarget(db: AppDb, userId: string): UserRow {
  const user = getUserById(db, userId)
  if (user == null) {
    throw new UserNotFoundError(`user not found: ${userId}`)
  }
  return user
}

function requireNotFinalActiveAdmin(db: AppDb, target: UserRow, nextType: UserType, nextStatus: UserStatus): void {
  if (
    target.userType === "admin"
    && target.status === "active"
    && (nextType !== "admin" || nextStatus !== "active")
    && countActiveAdmins(db) <= 1
  ) {
    throw new LastActiveAdminError("cannot remove the final active admin")
  }
}

export function createAdminService(deps: { db: AppDb; auditService?: AuditService }) {
  const { db } = deps
  const auditService = deps.auditService ?? createAuditService({ db })

  function auditContext(input: { actorUserId: string; actorCredentialId: string | null; requestId: string }): AuditContext {
    return createAuditContext(input.actorUserId, input.actorCredentialId, input.requestId)
  }

  function revokeAttachmentAccess(sessionId: string, attachmentId: string): void {
    const attachment = getAttachmentById(db, attachmentId)
    if (attachment == null || attachment.sessionId !== sessionId) {
      throw new GuestAttachmentNotFoundError(`guest attachment not found: ${attachmentId}`)
    }
    if (attachment.status !== "disconnected") {
      updateAttachmentStatus(db, { id: attachment.id, status: "disconnected" })
    }
    revokeAttachmentCredentials(db, {
      sessionId,
      attachmentId,
      revokedAt: new Date().toISOString(),
    })
    setAttachmentTransportTeardownPending(db, { id: attachmentId, pending: true })
  }

  return {
    requireAdmin(userId: string): void {
      requireActiveAdmin(db, userId)
    },

    listUsers(input: {
      actorUserId: string
      limit: number
      cursor: string | null
    }): { items: ReturnType<typeof toUserDto>[]; nextCursor: string | null } {
      requireActiveAdmin(db, input.actorUserId)
      const page = listAccountUsers(db, input)
      return { items: page.items.map(toUserDto), nextCursor: page.nextCursor }
    },

    listCredentials(input: { actorUserId: string; targetUserId: string }): AdminCredentialDto[] {
      requireActiveAdmin(db, input.actorUserId)
      requireTarget(db, input.targetUserId)
      return listAccountCredentialsForUser(db, input.targetUserId).map(toAdminCredentialDto)
    },

    listInstances(input: { actorUserId: string }): AdminInstanceDto[] {
      requireActiveAdmin(db, input.actorUserId)
      return listInstances(db).map(toAdminInstanceDto)
    },

    listAudit(input: { actorUserId: string; limit: number; cursor: string | null }) {
      requireActiveAdmin(db, input.actorUserId)
      return auditService.list(input)
    },

    async revokeAccountCredential(input: {
      actorUserId: string
      actorCredentialId: string
      requestId: string
      targetUserId: string
      credentialId: string
    }): Promise<void> {
      const audit = auditContext(input)
      const transaction = db.$client.transaction(() => {
        requireActiveAdmin(db, input.actorUserId)
        requireTarget(db, input.targetUserId)
        const credential = getAccountCredentialForUser(db, {
          userId: input.targetUserId,
          credentialId: input.credentialId,
        })
        if (credential == null) {
          throw new AccountCredentialNotFoundError(`account credential not found: ${input.credentialId}`)
        }
        revokeCredential(db, { id: credential.id, revokedAt: new Date().toISOString() })
        auditService.record({
          ...audit,
          action: "user.credential.revoke",
          targetType: "credential",
          targetId: credential.id,
          outcome: "success",
        })
      })
      transaction.immediate()
    },

    async setUserType(input: {
      actorUserId: string
      actorCredentialId: string | null
      requestId: string
      targetUserId: string
      type: Exclude<UserType, "guest">
    }): Promise<void> {
      const audit = auditContext(input)
      const transaction = db.$client.transaction(() => {
        requireActiveAdmin(db, input.actorUserId)
        const target = requireTarget(db, input.targetUserId)
        requireNotFinalActiveAdmin(db, target, input.type, target.status)
        updateUserType(db, { id: target.id, userType: input.type })
        auditService.record({
          ...audit,
          action: input.type === "admin" ? "user.promote" : "user.demote",
          targetType: "user",
          targetId: target.id,
          outcome: "success",
        })
      })
      transaction.immediate()
    },

    async setBanStatus(input: {
      actorUserId: string
      actorCredentialId: string | null
      requestId: string
      targetUserId: string
      status: UserStatus
    }): Promise<Array<{ sessionId: string; attachmentId: string }>> {
      const audit = auditContext(input)
      const teardownTargets = (() => {
        const affected = listAttachmentsForUser(db, input.targetUserId)
        const ownerSessionIds = new Set(affected.filter((attachment) => attachment.role === "owner").map((attachment) => attachment.sessionId))
        return affected.flatMap((attachment) => ownerSessionIds.has(attachment.sessionId)
          ? listAttachmentsForSession(db, attachment.sessionId).map((member) => ({ sessionId: attachment.sessionId, attachmentId: member.id }))
          : [{ sessionId: attachment.sessionId, attachmentId: attachment.id }])
          .filter((target, index, targets) => targets.findIndex((other) => other.attachmentId === target.attachmentId) === index)
      })()
      const transaction = db.$client.transaction(() => {
        requireActiveAdmin(db, input.actorUserId)
        const target = requireTarget(db, input.targetUserId)
        requireNotFinalActiveAdmin(db, target, target.userType, input.status)
        setUserStatus(db, { id: target.id, status: input.status })
        if (input.status === "banned") {
          const affected = listAttachmentsForUser(db, target.id)
          const ownerSessionIds = new Set(
            affected.filter((attachment) => attachment.role === "owner").map((attachment) => attachment.sessionId),
          )
          const teardownTargets: Array<{ sessionId: string; attachmentId: string }> = []
          for (const sessionId of ownerSessionIds) {
            // Owner bans are administrative Session revocations: every attachment loses
            // access, teardown is durable, and Instance desired power stays untouched.
            for (const attachment of listAttachmentsForSession(db, sessionId)) {
              revokeAttachmentAccess(sessionId, attachment.id)
              teardownTargets.push({ sessionId, attachmentId: attachment.id })
            }
            closeSession(db, { id: sessionId })
            auditService.record({
              ...audit,
              action: "session.close",
              targetType: "session",
              targetId: sessionId,
              outcome: "success",
            })
          }
          for (const attachment of affected) {
            if (!ownerSessionIds.has(attachment.sessionId)) {
              revokeAttachmentAccess(attachment.sessionId, attachment.id)
              teardownTargets.push({ sessionId: attachment.sessionId, attachmentId: attachment.id })
            }
          }
          auditService.record({
            ...audit,
            action: "user.ban",
            targetType: "user",
            targetId: target.id,
            outcome: "success",
          })
          return teardownTargets
        }
        auditService.record({
          ...audit,
          action: "user.unban",
          targetType: "user",
          targetId: target.id,
          outcome: "success",
        })
        return []
      })
      return await attachmentTransportCoordinator.runMany(teardownTargets, () => transaction.immediate())
    },

    async revokeGuestAttachment(input: {
      actorUserId: string
      actorCredentialId: string | null
      requestId: string
      sessionId: string
      attachmentId: string
    }): Promise<{ sessionId: string; attachmentId: string }> {
      const audit = auditContext(input)
      const transaction = db.$client.transaction(() => {
        requireActiveAdmin(db, input.actorUserId)
        const attachment = getAttachmentById(db, input.attachmentId)
        if (attachment == null || attachment.sessionId !== input.sessionId || attachment.userId == null) {
          throw new GuestAttachmentNotFoundError(`guest attachment not found: ${input.attachmentId}`)
        }
        const user = requireTarget(db, attachment.userId)
        if (user.userType !== "guest") {
          throw new GuestAttachmentNotFoundError(`guest attachment not found: ${input.attachmentId}`)
        }
        revokeAttachmentAccess(input.sessionId, input.attachmentId)
        auditService.record({
          ...audit,
          action: "admin.guest.revoke",
          targetType: "attachment",
          targetId: attachment.id,
          outcome: "success",
        })
        return { sessionId: input.sessionId, attachmentId: input.attachmentId }
      })
      return await attachmentTransportCoordinator.run(input, () => transaction.immediate())
    },
  }
}

export type AdminService = ReturnType<typeof createAdminService>
