import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createCredential } from "../src/persistence/repositories/credentials.js"
import { createPrincipal } from "../src/persistence/repositories/principals.js"
import { hashToken } from "../src/services/token.js"
import {
  createAuthService,
  NotServerOwnerError,
  PairingInviteExpiredError,
  PairingInviteInvalidError,
  ServerAlreadyClaimedError,
} from "../src/services/auth-service.js"

describe("auth service — claim and authenticate", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("claims an unclaimed server exactly once", async () => {
    const authService = createAuthService({ db: createTestDb() })

    const { principal, token } = await authService.claimServer({ displayName: "Server Owner" })
    expect(principal.isServerOwner).toBe(true)
    expect(principal.status).toBe("active")
    expect(token.length).toBeGreaterThanOrEqual(32)

    await expect(authService.claimServer({ displayName: "Someone Else" }))
      .rejects.toThrow(ServerAlreadyClaimedError)
  })

  it("authenticates a valid token and rejects an unknown one", async () => {
    const authService = createAuthService({ db: createTestDb() })
    const { principal, token } = await authService.claimServer({ displayName: "Server Owner" })

    const resolved = await authService.authenticate(token)
    expect(resolved).toMatchObject({ principalId: principal.id })

    const rejected = await authService.authenticate("not-a-real-token")
    expect(rejected).toBeNull()
  })

  it("rejects authentication for a revoked credential", async () => {
    const authService = createAuthService({ db: createTestDb() })
    const { token } = await authService.claimServer({ displayName: "Server Owner" })

    const resolved = await authService.authenticate(token)
    expect(resolved).not.toBeNull()

    await authService.revokeCredential({
      requesterPrincipalId: resolved!.principalId,
      credentialId: resolved!.credentialId,
    })

    expect(await authService.authenticate(token)).toBeNull()
  })

  it("rejects authentication for a banned principal", async () => {
    const db = createTestDb()
    const authService = createAuthService({ db })
    const { principal: owner } = await authService.claimServer({ displayName: "Server Owner" })

    // Seed a second principal + credential directly via the repository layer —
    // pairing-invite-based onboarding is implemented in Task 5, not this task.
    createPrincipal(db, { id: "principal_2", displayName: "Regular User", isServerOwner: false, status: "active" })
    const secondToken = "second-raw-token"
    createCredential(db, {
      id: "credential_2",
      principalId: "principal_2",
      tokenHash: hashToken(secondToken),
      deviceLabel: "phone",
      revokedAt: null,
    })

    expect(await authService.authenticate(secondToken)).toMatchObject({ principalId: "principal_2" })

    await authService.setPrincipalBanStatus({
      requesterPrincipalId: owner.id,
      targetPrincipalId: "principal_2",
      status: "banned",
    })

    expect(await authService.authenticate(secondToken)).toBeNull()
  })
})

describe("auth service — pairing invites", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("issues a pairing invite and redeems it into a new principal", async () => {
    const authService = createAuthService({ db: createTestDb() })
    const { principal: owner } = await authService.claimServer({ displayName: "Server Owner" })

    const { token: inviteToken } = await authService.createPairingInvite({
      requesterPrincipalId: owner.id,
      ttlMs: 60_000,
    })

    const { principal, token } = await authService.redeemPairingInvite({
      token: inviteToken,
      displayName: "New User",
      deviceLabel: "phone",
    })

    expect(principal.isServerOwner).toBe(false)
    expect(await authService.authenticate(token)).toMatchObject({ principalId: principal.id })
  })

  it("rejects a pairing invite created by a non-owner", async () => {
    const db = createTestDb()
    const authService = createAuthService({ db })
    await authService.claimServer({ displayName: "Server Owner" })

    createPrincipal(db, { id: "not_owner", displayName: "Not Owner", isServerOwner: false, status: "active" })

    await expect(authService.createPairingInvite({ requesterPrincipalId: "not_owner", ttlMs: 60_000 }))
      .rejects.toThrow(NotServerOwnerError)
  })

  it("rejects redeeming an unknown, expired, or already-consumed invite", async () => {
    const authService = createAuthService({ db: createTestDb() })
    const { principal: owner } = await authService.claimServer({ displayName: "Server Owner" })

    await expect(authService.redeemPairingInvite({ token: "unknown", displayName: "X", deviceLabel: "d" }))
      .rejects.toThrow(PairingInviteInvalidError)

    const { token: expiring } = await authService.createPairingInvite({
      requesterPrincipalId: owner.id,
      ttlMs: -1,
    })
    await expect(authService.redeemPairingInvite({ token: expiring, displayName: "X", deviceLabel: "d" }))
      .rejects.toThrow(PairingInviteExpiredError)

    const { token: reusable } = await authService.createPairingInvite({
      requesterPrincipalId: owner.id,
      ttlMs: 60_000,
    })
    await authService.redeemPairingInvite({ token: reusable, displayName: "First", deviceLabel: "d" })
    await expect(authService.redeemPairingInvite({ token: reusable, displayName: "Second", deviceLabel: "d" }))
      .rejects.toThrow(PairingInviteInvalidError)
  })
})
