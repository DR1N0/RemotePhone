import { getConnInfo } from "@hono/node-server/conninfo"
import type { Context, MiddlewareHandler } from "hono"

import type { AuthenticatedPrincipal, AuthService } from "../services/auth-service.js"

const PRINCIPAL_CONTEXT_KEY = "principal"

export function isLoopbackAddress(address: string | undefined): boolean {
  if (address == null) {
    return false
  }
  return address === "127.0.0.1" || address === "::1" || address === "::ffff:127.0.0.1"
}

export function defaultGetClientAddress(c: Context): string | undefined {
  try {
    return getConnInfo(c).remote.address
  } catch {
    return undefined
  }
}

export function getAuthenticatedPrincipal(c: Context): AuthenticatedPrincipal {
  const principal = c.get(PRINCIPAL_CONTEXT_KEY) as AuthenticatedPrincipal | undefined
  if (principal == null) {
    throw new Error("getAuthenticatedPrincipal called without createBearerAuthMiddleware in the request chain")
  }
  return principal
}

export function createBearerAuthMiddleware(authService: AuthService): MiddlewareHandler {
  return async (c, next) => {
    const header = c.req.header("Authorization")
    const token = header?.startsWith("Bearer ") ? header.slice("Bearer ".length) : undefined
    if (token == null || token.length === 0) {
      return c.json({ error: "missing bearer token" }, 401)
    }

    const principal = await authService.authenticate(token)
    if (principal == null) {
      return c.json({ error: "invalid or revoked credential" }, 401)
    }

    c.set(PRINCIPAL_CONTEXT_KEY, principal)
    await next()
  }
}
