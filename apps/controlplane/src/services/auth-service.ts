import { randomUUID } from "node:crypto"

import type { AppDb } from "../persistence/db.js"
import {
  createCredential,
  getCredentialById,
  getCredentialByTokenHash,
  revokeCredential as revokeCredentialRow,
} from "../persistence/repositories/credentials.js"
import { getAttachmentById } from "../persistence/repositories/attachments.js"
import {
  createPairingInvite as createRegistrationInviteRow,
  consumePairingInvite,
  getPairingInviteByTokenHash,
} from "../persistence/repositories/pairing-invites.js"
import { getSessionById } from "../persistence/repositories/sessions.js"
import {
  countRegisteredOrAdminUsers,
  createUser,
  enrollUser,
  getUserById,
  getUserByUsername,
  normalizeUsername,
  toUserDto,
} from "../persistence/repositories/users.js"
import type { CredentialScope, UserDto } from "../persistence/schema.js"
import { createBootstrapSecretService } from "./bootstrap-secret.js"
import { createAuditContext, createAuditService, type AuditService } from "./audit-service.js"
import { hashPassword, verifyPassword } from "./password.js"
import { generateToken, hashToken } from "./token.js"

export class AuthServiceError extends Error {}
export class ServerAlreadyClaimedError extends AuthServiceError {}
export class BootstrapSecretInvalidError extends AuthServiceError {}
export class BootstrapSecretUnavailableError extends AuthServiceError {}
export class InvalidLoginError extends AuthServiceError {}
export class UserNotFoundError extends AuthServiceError {}
export class UserBannedError extends AuthServiceError {}
export class CredentialNotFoundError extends AuthServiceError {}
export class RegistrationInviteInvalidError extends AuthServiceError {}
export class UsernameConflictError extends AuthServiceError {}
export class LegacyEnrollmentError extends AuthServiceError {}
export class NotAdminError extends AuthServiceError {}

export type AuthenticatedUser = {
  userId: string
  credentialId: string
  scope: CredentialScope
  sessionId: string | null
  attachmentId: string | null
}

export type AuthService = ReturnType<typeof createAuthService>

const ACCOUNT_CREDENTIAL_TTL_MS = 30 * 24 * 60 * 60 * 1000
const ATTACHMENT_CREDENTIAL_MAX_TTL_MS = 24 * 60 * 60 * 1000

function requireActiveAdmin(db: AppDb, userId: string): void {
  const user = getUserById(db, userId)
  if (user == null) {
    throw new UserNotFoundError(`user not found: ${userId}`)
  }
  if (user.userType !== "admin" || user.status !== "active") {
    throw new NotAdminError(`user is not an active admin: ${userId}`)
  }
}

function isValidUsername(username: string): boolean {
  return /^[a-z0-9_]{3,32}$/.test(normalizeUsername(username))
}

function isValidPassword(password: string): boolean {
  return password.length >= 12 && password.length <= 1024
}

export function createAuthService(deps: { db: AppDb; now?: () => Date; bootstrapSecretPath?: string; auditService?: AuditService }) {
  const { db } = deps
  const auditService = deps.auditService ?? createAuditService({ db })
  const now = deps.now ?? (() => new Date())
  const bootstrapSecret = deps.bootstrapSecretPath == null
    ? undefined
    : createBootstrapSecretService(deps.bootstrapSecretPath)

  if (bootstrapSecret != null && countRegisteredOrAdminUsers(db) === 0) {
    bootstrapSecret.getOrCreate()
  }

  function requireActiveUser(userId: string, scope: CredentialScope): void {
    const user = getUserById(db, userId)
    if (user == null) {
      throw new AuthServiceError(`user not found: ${userId}`)
    }
    if (user.status === "banned") {
      throw new UserBannedError(`user is banned: ${userId}`)
    }
    if (scope === "account" && user.userType === "guest") {
      throw new AuthServiceError("guest users cannot receive account credentials")
    }
  }

  function createScopedCredential(input: {
    userId: string
    deviceLabel: string
    scope: CredentialScope
    sessionId: string | null
    attachmentId: string | null
    notBefore: string
    expiresAt: string | null
  }): string {
    const token = generateToken()
    createCredential(db, {
      id: randomUUID(),
      userId: input.userId,
      tokenHash: hashToken(token),
      deviceLabel: input.deviceLabel,
      scope: input.scope,
      sessionId: input.sessionId,
      attachmentId: input.attachmentId,
      notBefore: input.notBefore,
      expiresAt: input.expiresAt,
      revokedAt: null,
    })
    return token
  }

  function issueAccountCredential(userId: string, deviceLabel: string): string {
    requireActiveUser(userId, "account")
    const issuedAt = now()
    return createScopedCredential({
      userId,
      deviceLabel,
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: issuedAt.toISOString(),
      expiresAt: new Date(issuedAt.getTime() + ACCOUNT_CREDENTIAL_TTL_MS).toISOString(),
    })
  }

  function issueAttachmentCredential(input: {
    userId: string
    sessionId: string
    attachmentId: string
    expiresAt: string
  }): string {
    requireActiveUser(input.userId, "attachment")

    const issuedAt = now()
    const expiresAt = new Date(input.expiresAt)
    if (
      Number.isNaN(expiresAt.getTime())
      || expiresAt.getTime() <= issuedAt.getTime()
      || expiresAt.getTime() > issuedAt.getTime() + ATTACHMENT_CREDENTIAL_MAX_TTL_MS
    ) {
      throw new AuthServiceError("attachment credential expiration must be within 24 hours")
    }

    const session = getSessionById(db, input.sessionId)
    if (session == null || session.status !== "active") {
      throw new AuthServiceError(`session is not active: ${input.sessionId}`)
    }

    const attachment = getAttachmentById(db, input.attachmentId)
    if (
      attachment == null
      || attachment.sessionId !== input.sessionId
      || attachment.userId !== input.userId
    ) {
      throw new AuthServiceError("attachment is not bound to the credential user and session")
    }

    return createScopedCredential({
      userId: input.userId,
      deviceLabel: "attachment",
      scope: "attachment",
      sessionId: input.sessionId,
      attachmentId: input.attachmentId,
      notBefore: issuedAt.toISOString(),
      expiresAt: input.expiresAt,
    })
  }

  // The caller owns the surrounding transaction so attachment state and credential rotation commit together.
  function rotateAttachmentCredential(input: {
    credentialId: string
    userId: string
    sessionId: string
    attachmentId: string
  }): string {
    const credential = getCredentialById(db, input.credentialId)
    if (
      credential == null
      || credential.revokedAt != null
      || credential.scope !== "attachment"
      || credential.userId !== input.userId
      || credential.sessionId !== input.sessionId
      || credential.attachmentId !== input.attachmentId
    ) {
      throw new CredentialNotFoundError("attachment credential is not valid for rotation")
    }

    const expiresAt = new Date(credential.expiresAt ?? "").getTime()
    if (Number.isNaN(expiresAt) || expiresAt <= now().getTime()) {
      throw new CredentialNotFoundError("attachment credential is expired")
    }

    const attachmentToken = issueAttachmentCredential({
      userId: input.userId,
      sessionId: input.sessionId,
      attachmentId: input.attachmentId,
      expiresAt: new Date(now().getTime() + ATTACHMENT_CREDENTIAL_MAX_TTL_MS).toISOString(),
    })
    revokeCredentialRow(db, { id: credential.id, revokedAt: now().toISOString() })
    return attachmentToken
  }

  return {
    issueAccountCredential,
    issueAttachmentCredential,
    rotateAttachmentCredential,

    getSetupStatus(): { claimed: boolean } {
      return { claimed: countRegisteredOrAdminUsers(db) > 0 }
    },

    async setupAdmin(input: {
      username: string
      password: string
      deviceLabel: string
      bootstrapSecret: string
    }): Promise<{ user: UserDto; token: string }> {
      if (countRegisteredOrAdminUsers(db) > 0) {
        throw new ServerAlreadyClaimedError("server has already been claimed")
      }
      if (bootstrapSecret == null) {
        throw new BootstrapSecretUnavailableError("bootstrap secret path is not configured")
      }
      if (!bootstrapSecret.matches(input.bootstrapSecret)) {
        throw new BootstrapSecretInvalidError("bootstrap secret is invalid")
      }

      const passwordHash = await hashPassword(input.password)
      const result = db.$client.transaction(() => {
        if (countRegisteredOrAdminUsers(db) > 0) {
          throw new ServerAlreadyClaimedError("server has already been claimed")
        }

        const userId = randomUUID()
        createUser(db, {
          id: userId,
          displayName: input.username,
          userType: "admin",
          username: input.username,
          passwordHash,
          enrollmentRequired: false,
          isServerOwner: true,
          status: "active",
        })
        const user = getUserById(db, userId)
        if (user == null) {
          throw new UserNotFoundError(`user not found after creation: ${userId}`)
        }
        return { user: toUserDto(user), token: issueAccountCredential(userId, input.deviceLabel) }
      })
      const setupTransaction = result.immediate()

      bootstrapSecret.invalidate(input.bootstrapSecret)
      return setupTransaction
    },

    async login(input: {
      username: string
      password: string
      deviceLabel: string
    }): Promise<{ user: UserDto; token: string }> {
      const user = getUserByUsername(db, normalizeUsername(input.username))
      if (
        user == null
        || user.status !== "active"
        || user.enrollmentRequired
        || user.passwordHash == null
        || !await verifyPassword(input.password, user.passwordHash)
      ) {
        throw new InvalidLoginError("invalid username or password")
      }

      return { user: toUserDto(user), token: issueAccountCredential(user.id, input.deviceLabel) }
    },

    getUser(userId: string): UserDto {
      const user = getUserById(db, userId)
      if (user == null) {
        throw new UserNotFoundError(`user not found: ${userId}`)
      }
      return toUserDto(user)
    },

    async revokeCurrentCredential(credentialId: string): Promise<void> {
      const credential = getCredentialById(db, credentialId)
      if (credential == null) {
        throw new CredentialNotFoundError(`credential not found: ${credentialId}`)
      }
      revokeCredentialRow(db, { id: credentialId, revokedAt: now().toISOString() })
    },

    async authenticate(token: string): Promise<AuthenticatedUser | null> {
      const credential = getCredentialByTokenHash(db, hashToken(token))
      if (credential == null || credential.revokedAt != null) {
        return null
      }

      const currentTime = now().getTime()
      const notBefore = credential.notBefore == null ? null : new Date(credential.notBefore).getTime()
      const expiresAt = credential.expiresAt == null ? null : new Date(credential.expiresAt).getTime()
      if (
        (notBefore != null && (Number.isNaN(notBefore) || notBefore > currentTime))
        || (expiresAt != null && (Number.isNaN(expiresAt) || expiresAt <= currentTime))
      ) {
        return null
      }

      const user = getUserById(db, credential.userId)
      if (user == null || user.status === "banned") {
        return null
      }

      if (credential.scope === "account") {
        if (user.userType === "guest" || credential.sessionId != null || credential.attachmentId != null) {
          return null
        }
      } else if (credential.scope === "attachment") {
        if (credential.sessionId == null || credential.attachmentId == null) {
          return null
        }

        const session = getSessionById(db, credential.sessionId)
        const attachment = getAttachmentById(db, credential.attachmentId)
        if (
          session == null
          || session.status !== "active"
          || attachment == null
          || attachment.sessionId !== session.id
          || attachment.userId !== user.id
        ) {
          return null
        }
      } else {
        return null
      }

      return {
        userId: user.id,
        credentialId: credential.id,
        scope: credential.scope,
        sessionId: credential.sessionId,
        attachmentId: credential.attachmentId,
      }
    },

    async revokeCredential(input: { requesterUserId: string; credentialId: string }): Promise<void> {
      const credential = getCredentialById(db, input.credentialId)
      if (credential == null) {
        throw new CredentialNotFoundError(`credential not found: ${input.credentialId}`)
      }
      if (credential.userId !== input.requesterUserId) {
        throw new AuthServiceError("a credential can only be revoked by its own user")
      }

      revokeCredentialRow(db, { id: input.credentialId, revokedAt: now().toISOString() })
    },

    async createRegistrationInvite(input: {
      actorUserId: string
      actorCredentialId: string | null
      requestId: string
      ttlMs?: number
    }): Promise<{ token: string; expiresAt: string }> {
      requireActiveAdmin(db, input.actorUserId)
      const ttlMs = input.ttlMs ?? 24 * 60 * 60_000
      if (!Number.isInteger(ttlMs) || ttlMs < 5 * 60_000 || ttlMs > 7 * 24 * 60 * 60_000) {
        throw new AuthServiceError("registration invite TTL must be between 5 minutes and 7 days")
      }

      const token = generateToken()
      const expiresAt = new Date(now().getTime() + ttlMs).toISOString()
      const inviteId = randomUUID()
      return db.$client.transaction(() => {
        createRegistrationInviteRow(db, {
          id: inviteId,
          tokenHash: hashToken(token),
          createdBy: input.actorUserId,
          expiresAt,
          consumedAt: null,
          consumedByUserId: null,
        })
        auditService.record({
          ...createAuditContext(input.actorUserId, input.actorCredentialId, input.requestId),
          action: "registration.invite.issue",
          targetType: "registration_invite",
          targetId: inviteId,
          outcome: "success",
        })
        return { token, expiresAt }
      })()
    },

    async register(input: {
      inviteToken: string
      username: string
      password: string
      deviceLabel: string
      requestId: string
    }): Promise<{ user: UserDto; token: string }> {
      const username = normalizeUsername(input.username)
      if (!isValidUsername(username) || !isValidPassword(input.password)) {
        throw new AuthServiceError("invalid registration credentials")
      }
      const inviteTokenHash = hashToken(input.inviteToken)
      const precheckAt = now().toISOString()
      const precheckedInvite = getPairingInviteByTokenHash(db, inviteTokenHash)
      if (
        precheckedInvite == null
        || precheckedInvite.consumedAt != null
        || new Date(precheckedInvite.expiresAt).getTime() <= new Date(precheckAt).getTime()
      ) {
        throw new RegistrationInviteInvalidError("registration invite is invalid or expired")
      }

      const passwordHash = await hashPassword(input.password)
      const userId = randomUUID()
      const transaction = db.$client.transaction(() => {
        const registeredAt = now().toISOString()
        const invite = getPairingInviteByTokenHash(db, inviteTokenHash)
        if (
          invite == null
          || invite.consumedAt != null
          || new Date(invite.expiresAt).getTime() <= new Date(registeredAt).getTime()
        ) {
          throw new RegistrationInviteInvalidError("registration invite is invalid or expired")
        }
        if (getUserByUsername(db, username) != null) {
          throw new UsernameConflictError("username is already registered")
        }

        createUser(db, {
          id: userId,
          displayName: username,
          userType: "registered",
          username,
          passwordHash,
          enrollmentRequired: false,
          isServerOwner: false,
          status: "active",
        })
        if (!consumePairingInvite(db, {
          id: invite.id,
          consumedAt: registeredAt,
          consumedByUserId: userId,
          now: registeredAt,
        })) {
          throw new RegistrationInviteInvalidError("registration invite is invalid or expired")
        }
        auditService.record({
          ...createAuditContext(userId, null, input.requestId),
          action: "registration.invite.redeem",
          targetType: "registration_invite",
          targetId: invite.id,
          outcome: "success",
        })
        const user = getUserById(db, userId)
        if (user == null) {
          throw new UserNotFoundError(`user not found after registration: ${userId}`)
        }
        return { user: toUserDto(user), token: issueAccountCredential(userId, input.deviceLabel) }
      })
      return transaction.immediate()
    },

    async enrollLegacy(input: { userId: string; username: string; password: string }): Promise<UserDto> {
      const username = normalizeUsername(input.username)
      if (!isValidUsername(username) || !isValidPassword(input.password)) {
        throw new LegacyEnrollmentError("invalid enrollment credentials")
      }
      const passwordHash = await hashPassword(input.password)
      const transaction = db.$client.transaction(() => {
        const user = getUserById(db, input.userId)
        if (
          user == null
          || user.status !== "active"
          || user.userType !== "registered"
          || !user.enrollmentRequired
          || user.passwordHash != null
        ) {
          throw new LegacyEnrollmentError("user is not eligible for legacy enrollment")
        }
        const existing = getUserByUsername(db, username)
        if (existing != null && existing.id !== user.id) {
          throw new LegacyEnrollmentError("username is already registered")
        }
        enrollUser(db, { id: user.id, username, passwordHash })
        const enrolled = getUserById(db, user.id)
        if (enrolled == null) {
          throw new UserNotFoundError(`user not found after enrollment: ${user.id}`)
        }
        return toUserDto(enrolled)
      })
      try {
        return transaction.immediate()
      } catch (error) {
        if (error instanceof Error && error.message.includes("UNIQUE constraint failed: users.username")) {
          throw new LegacyEnrollmentError("username is already registered")
        }
        throw error
      }
    },
  }
}
