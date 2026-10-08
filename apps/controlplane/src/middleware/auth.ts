import { getConnInfo } from "@hono/node-server/conninfo"
import type { Context, MiddlewareHandler } from "hono"

import type {
  AuthenticatedUser,
  AuthService,
} from "../services/auth-service.js"

const AUTHENTICATED_USER_CONTEXT_KEY = "authenticatedUser"

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

export function getAuthenticatedUser(c: Context): AuthenticatedUser {
  const user = c.get(AUTHENTICATED_USER_CONTEXT_KEY) as AuthenticatedUser | undefined
  if (user == null) {
    throw new Error("getAuthenticatedUser called without createBearerAuthMiddleware in the request chain")
  }
  return user
}


export function createBearerAuthMiddleware(authService: AuthService): MiddlewareHandler {
  return async (c, next) => {
    const header = c.req.header("Authorization")
    const token = header?.startsWith("Bearer ") ? header.slice("Bearer ".length) : undefined
    if (token == null || token.length === 0) {
      return c.json({ error: "missing bearer token" }, 401)
    }

    const user = await authService.authenticate(token)
    if (user == null) {
      return c.json({ error: "invalid or revoked credential" }, 401)
    }

    c.set(AUTHENTICATED_USER_CONTEXT_KEY, user)
    await next()
  }
}

export function createScopedAuthMiddleware(scope: AuthenticatedUser["scope"]): MiddlewareHandler {
  return async (c, next) => {
    if (getAuthenticatedUser(c).scope !== scope) {
      return c.json({ error: "credential scope is not authorized" }, 403)
    }
    await next()
  }
}

export const requireAccountAuth = createScopedAuthMiddleware("account")
export const requireAttachmentAuth = createScopedAuthMiddleware("attachment")
