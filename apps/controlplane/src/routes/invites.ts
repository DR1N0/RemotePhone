import { randomUUID } from "node:crypto"

import type { Context, Hono } from "hono"

import {
  createBearerAuthMiddleware,
  getAuthenticatedUser,
  requireAccountAuth,
} from "../middleware/auth.js"
import type { AuthService } from "../services/auth-service.js"
import {
  SessionForbiddenError,
  SessionInvalidRequestError,
  SessionNotFoundError,
} from "../services/session-service.js"
import {
  SessionInviteInvalidError,
  type SessionInviteService,
} from "../services/session-invite-service.js"

function invalidRequest(c: Context, message: string) {
  return c.json({ error: message }, 400)
}

function issueErrorStatus(error: unknown): 400 | 403 | 404 | 500 {
  if (error instanceof SessionInvalidRequestError || error instanceof SessionInviteInvalidError) {
    return 400
  }
  if (error instanceof SessionForbiddenError) {
    return 403
  }
  if (error instanceof SessionNotFoundError) {
    return 404
  }
  return 500
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "unexpected error"
}

export function registerInviteRoutes(
  app: Hono,
  deps: {
    sessionInviteService: SessionInviteService
    authService: AuthService
  },
): void {
  const requireAuth = createBearerAuthMiddleware(deps.authService)

  app.post("/api/v1/sessions/:id/invites", requireAuth, requireAccountAuth, async (c) => {
    let body: { ttlMs?: unknown } = {}
    try {
      body = await c.req.json() as { ttlMs?: unknown }
    } catch {
      // An omitted body selects the default ten-minute TTL.
    }

    try {
      const caller = getAuthenticatedUser(c)
      const invite = await deps.sessionInviteService.createSessionInvite({
        sessionId: c.req.param("id"),
        actorUserId: caller.userId,
        actorCredentialId: caller.credentialId,
        requestId: randomUUID(),
        ttlMs: body.ttlMs as number | undefined,
      })
      c.status(201)
      return c.json(invite)
    } catch (error) {
      return c.json({ error: errorMessage(error) }, issueErrorStatus(error))
    }
  })

  app.post("/api/v1/session-invites/redeem", async (c) => {
    let body: { token?: unknown; mode?: unknown; displayName?: unknown }
    try {
      body = await c.req.json() as { token?: unknown; mode?: unknown; displayName?: unknown }
    } catch {
      return invalidRequest(c, "a JSON body is required")
    }
    if (typeof body.token !== "string") {
      return invalidRequest(c, "token is required")
    }

    if (body.mode === "guest") {
      if (c.req.header("Authorization") != null) {
        return invalidRequest(c, "guest redemption must not include an Authorization header")
      }
      if (body.displayName != null && typeof body.displayName !== "string") {
        return invalidRequest(c, "displayName must be a string")
      }
      if (
        typeof body.displayName === "string"
        && (body.displayName.length < 1 || body.displayName.length > 40 || body.displayName.trim().length === 0)
      ) {
        return invalidRequest(c, "displayName must be 1 to 40 non-whitespace characters")
      }
      try {
        const joined = await deps.sessionInviteService.redeemSessionInvite({
          token: body.token,
          mode: "guest",
          actorCredentialId: null,
          requestId: randomUUID(),
          displayName: typeof body.displayName === "string" ? body.displayName : undefined,
        })
        c.status(201)
        return c.json(joined)
      } catch (error) {
        return c.json({ error: errorMessage(error) }, issueErrorStatus(error))
      }
    }

    if (body.mode !== "registered") {
      return invalidRequest(c, "mode must be registered or guest")
    }
    const header = c.req.header("Authorization")
    const token = header?.startsWith("Bearer ") ? header.slice("Bearer ".length) : undefined
    if (token == null || token.length === 0) {
      return c.json({ error: "missing bearer token" }, 401)
    }
    const authenticated = await deps.authService.authenticate(token)
    if (authenticated == null) {
      return c.json({ error: "invalid or revoked credential" }, 401)
    }
    if (authenticated.scope !== "account") {
      return c.json({ error: "credential scope is not authorized" }, 403)
    }
    try {
      const joined = await deps.sessionInviteService.redeemSessionInvite({
        token: body.token,
        mode: "registered",
        requesterUserId: authenticated.userId,
        actorCredentialId: authenticated.credentialId,
        requestId: randomUUID(),
      })
      c.status(201)
      return c.json(joined)
    } catch (error) {
      return c.json({ error: errorMessage(error) }, issueErrorStatus(error))
    }
  })
}
