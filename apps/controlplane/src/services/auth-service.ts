import { randomUUID } from "node:crypto"

import type { AppDb } from "../persistence/db.js"
import {
  createCredential,
  getCredentialById,
  getCredentialByTokenHash,
  revokeCredential as revokeCredentialRow,
} from "../persistence/repositories/credentials.js"
import {
  createPairingInvite as createPairingInviteRow,
  consumePairingInvite,
  getPairingInviteByTokenHash,
} from "../persistence/repositories/pairing-invites.js"
import {
  createPrincipal,
  getPrincipalById,
  getServerOwnerPrincipal,
  setPrincipalStatus,
} from "../persistence/repositories/principals.js"
import type { PrincipalRow, PrincipalStatus } from "../persistence/schema.js"
import { generateToken, hashToken } from "./token.js"

export class AuthServiceError extends Error {}
export class ServerAlreadyClaimedError extends AuthServiceError {}
export class PrincipalNotFoundError extends AuthServiceError {}
export class PrincipalBannedError extends AuthServiceError {}
export class CredentialNotFoundError extends AuthServiceError {}
export class NotServerOwnerError extends AuthServiceError {}
export class PairingInviteInvalidError extends AuthServiceError {}
export class PairingInviteExpiredError extends AuthServiceError {}

export type AuthenticatedPrincipal = {
  principalId: string
  credentialId: string
}

export type AuthService = ReturnType<typeof createAuthService>

function requireServerOwner(db: AppDb, principalId: string): PrincipalRow {
  const principal = getPrincipalById(db, principalId)
  if (principal == null) {
    throw new PrincipalNotFoundError(`principal not found: ${principalId}`)
  }
  if (!principal.isServerOwner) {
    throw new NotServerOwnerError(`principal is not the server owner: ${principalId}`)
  }
  return principal
}

export function createAuthService(deps: { db: AppDb; now?: () => Date }) {
  const { db } = deps
  const now = deps.now ?? (() => new Date())

  function issueCredential(principalId: string, deviceLabel: string): string {
    const token = generateToken()
    createCredential(db, {
      id: randomUUID(),
      principalId,
      tokenHash: hashToken(token),
      deviceLabel,
      revokedAt: null,
    })
    return token
  }

  return {
    async claimServer(input: { displayName: string }): Promise<{ principal: PrincipalRow; token: string }> {
      if (getServerOwnerPrincipal(db) != null) {
        throw new ServerAlreadyClaimedError("server has already been claimed")
      }

      const principalId = randomUUID()
      createPrincipal(db, {
        id: principalId,
        displayName: input.displayName,
        isServerOwner: true,
        status: "active",
      })

      const token = issueCredential(principalId, "claim")
      const principal = getPrincipalById(db, principalId)
      if (principal == null) {
        throw new PrincipalNotFoundError(`principal not found after creation: ${principalId}`)
      }

      return { principal, token }
    },

    async authenticate(token: string): Promise<AuthenticatedPrincipal | null> {
      const credential = getCredentialByTokenHash(db, hashToken(token))
      if (credential == null || credential.revokedAt != null) {
        return null
      }

      const principal = getPrincipalById(db, credential.principalId)
      if (principal == null || principal.status === "banned") {
        return null
      }

      return { principalId: principal.id, credentialId: credential.id }
    },

    async revokeCredential(input: { requesterPrincipalId: string; credentialId: string }): Promise<void> {
      const credential = getCredentialById(db, input.credentialId)
      if (credential == null) {
        throw new CredentialNotFoundError(`credential not found: ${input.credentialId}`)
      }
      if (credential.principalId !== input.requesterPrincipalId) {
        throw new AuthServiceError("a credential can only be revoked by its own principal")
      }

      revokeCredentialRow(db, { id: input.credentialId, revokedAt: now().toISOString() })
    },

    async setPrincipalBanStatus(input: {
      requesterPrincipalId: string
      targetPrincipalId: string
      status: PrincipalStatus
    }): Promise<void> {
      requireServerOwner(db, input.requesterPrincipalId)

      const target = getPrincipalById(db, input.targetPrincipalId)
      if (target == null) {
        throw new PrincipalNotFoundError(`principal not found: ${input.targetPrincipalId}`)
      }
      if (target.isServerOwner) {
        throw new AuthServiceError("the server owner principal cannot be banned")
      }

      setPrincipalStatus(db, { id: input.targetPrincipalId, status: input.status })
    },

    async createPairingInvite(input: {
      requesterPrincipalId: string
      ttlMs: number
    }): Promise<{ token: string; expiresAt: string }> {
      requireServerOwner(db, input.requesterPrincipalId)

      const token = generateToken()
      const expiresAt = new Date(now().getTime() + input.ttlMs).toISOString()
      createPairingInviteRow(db, {
        id: randomUUID(),
        tokenHash: hashToken(token),
        createdBy: input.requesterPrincipalId,
        expiresAt,
        consumedAt: null,
        consumedByPrincipalId: null,
      })

      return { token, expiresAt }
    },

    async redeemPairingInvite(input: {
      token: string
      displayName: string
      deviceLabel: string
    }): Promise<{ principal: PrincipalRow; token: string }> {
      const invite = getPairingInviteByTokenHash(db, hashToken(input.token))
      if (invite == null) {
        throw new PairingInviteInvalidError("pairing invite not found")
      }
      if (invite.consumedAt != null) {
        throw new PairingInviteInvalidError("pairing invite has already been used")
      }
      if (new Date(invite.expiresAt).getTime() <= now().getTime()) {
        throw new PairingInviteExpiredError("pairing invite has expired")
      }

      const principalId = randomUUID()
      createPrincipal(db, {
        id: principalId,
        displayName: input.displayName,
        isServerOwner: false,
        status: "active",
      })
      consumePairingInvite(db, {
        id: invite.id,
        consumedAt: now().toISOString(),
        consumedByPrincipalId: principalId,
      })

      const token = issueCredential(principalId, input.deviceLabel)
      const principal = getPrincipalById(db, principalId)
      if (principal == null) {
        throw new PrincipalNotFoundError(`principal not found after creation: ${principalId}`)
      }

      return { principal, token }
    },
  }
}
