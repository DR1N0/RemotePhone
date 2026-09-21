import type { Context, Hono } from "hono"

import {
  createBearerAuthMiddleware,
  defaultGetClientAddress,
  getAuthenticatedPrincipal,
  isLoopbackAddress,
} from "../middleware/auth.js"
import {
  CredentialNotFoundError,
  NotServerOwnerError,
  PairingInviteExpiredError,
  PairingInviteInvalidError,
  PrincipalNotFoundError,
  ServerAlreadyClaimedError,
  type AuthService,
} from "../services/auth-service.js"

export function registerAuthRoutes(
  app: Hono,
  deps: {
    authService: AuthService
    getClientAddress?: (c: Context) => string | undefined
  },
): void {
  const { authService } = deps
  const getClientAddress = deps.getClientAddress ?? defaultGetClientAddress
  const requireAuth = createBearerAuthMiddleware(authService)

  app.post("/api/v1/auth/claim", async (c) => {
    if (!isLoopbackAddress(getClientAddress(c))) {
      return c.json({ error: "server claim is only allowed from a loopback connection" }, 403)
    }

    const { displayName } = await c.req.json() as { displayName?: string }
    if (displayName == null || displayName.trim() === "") {
      return c.json({ error: "displayName is required" }, 400)
    }

    try {
      const { principal, token } = await authService.claimServer({ displayName })
      return c.json({ principal, token }, 201)
    } catch (e) {
      if (e instanceof ServerAlreadyClaimedError) {
        return c.json({ error: "server has already been claimed" }, 409)
      }
      throw e
    }
  })

  app.get("/api/v1/auth/me", requireAuth, (c) => {
    return c.json(getAuthenticatedPrincipal(c))
  })

  app.post("/api/v1/auth/pairing-invites", requireAuth, async (c) => {
    const { principalId } = getAuthenticatedPrincipal(c)
    const { ttlMinutes } = await c.req.json() as { ttlMinutes?: number }
    if (ttlMinutes == null || !Number.isFinite(ttlMinutes) || ttlMinutes <= 0) {
      return c.json({ error: "ttlMinutes must be a positive number" }, 400)
    }

    try {
      const { token, expiresAt } = await authService.createPairingInvite({
        requesterPrincipalId: principalId,
        ttlMs: ttlMinutes * 60_000,
      })
      return c.json({ token, expiresAt }, 201)
    } catch (e) {
      if (e instanceof NotServerOwnerError) {
        return c.json({ error: "only the server owner may issue pairing invites" }, 403)
      }
      throw e
    }
  })

  app.post("/api/v1/auth/pairing-invites/redeem", async (c) => {
    const { token, displayName, deviceLabel } = await c.req.json() as {
      token?: string
      displayName?: string
      deviceLabel?: string
    }
    if (token == null || displayName == null || deviceLabel == null) {
      return c.json({ error: "token, displayName, and deviceLabel are required" }, 400)
    }

    try {
      const { principal, token: credentialToken } = await authService.redeemPairingInvite({
        token,
        displayName,
        deviceLabel,
      })
      return c.json({ principal, token: credentialToken }, 201)
    } catch (e) {
      if (e instanceof PairingInviteInvalidError || e instanceof PairingInviteExpiredError) {
        return c.json({ error: e.message }, 400)
      }
      throw e
    }
  })

  app.post("/api/v1/auth/credentials/:id/revoke", requireAuth, async (c) => {
    const { principalId } = getAuthenticatedPrincipal(c)
    try {
      await authService.revokeCredential({ requesterPrincipalId: principalId, credentialId: c.req.param("id") })
      return c.body(null, 204)
    } catch (e) {
      if (e instanceof CredentialNotFoundError) {
        return c.json({ error: "credential not found" }, 404)
      }
      if (e instanceof Error && e.message.includes("only be revoked by its own principal")) {
        return c.json({ error: "credential not found" }, 404)
      }
      throw e
    }
  })

  async function setBanStatus(c: Context, targetPrincipalId: string, status: "active" | "banned") {
    const { principalId } = getAuthenticatedPrincipal(c)
    try {
      await authService.setPrincipalBanStatus({
        requesterPrincipalId: principalId,
        targetPrincipalId,
        status,
      })
      return c.body(null, 204)
    } catch (e) {
      if (e instanceof NotServerOwnerError) {
        return c.json({ error: "only the server owner may change ban status" }, 403)
      }
      if (e instanceof PrincipalNotFoundError) {
        return c.json({ error: "principal not found" }, 404)
      }
      if (e instanceof Error && e.message.includes("cannot be banned")) {
        return c.json({ error: e.message }, 400)
      }
      throw e
    }
  }

  app.post("/api/v1/principals/:id/ban", requireAuth, (c) => setBanStatus(c, c.req.param("id"), "banned"))
  app.post("/api/v1/principals/:id/unban", requireAuth, (c) => setBanStatus(c, c.req.param("id"), "active"))
}
