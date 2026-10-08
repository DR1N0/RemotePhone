import { randomUUID } from "node:crypto"

import type { Context, Hono } from "hono"

import {
  enrollRequestSchema,
  loginRequestSchema,
  registerRequestSchema,
  registrationInviteRequestSchema,
  setupAdminRequestSchema,
} from "../domain/schemas.js"
import {
  createBearerAuthMiddleware,
  defaultGetClientAddress,
  getAuthenticatedUser,
  isLoopbackAddress,
  requireAccountAuth,
} from "../middleware/auth.js"
import {
  AuthServiceError,
  BootstrapSecretInvalidError,
  CredentialNotFoundError,
  InvalidLoginError,
  LegacyEnrollmentError,
  NotAdminError,
  RegistrationInviteInvalidError,
  ServerAlreadyClaimedError,
  UsernameConflictError,
  type AuthService,
} from "../services/auth-service.js"

const LOGIN_FAILURE_LIMIT = 5
const LOGIN_FAILURE_WINDOW_MS = 10 * 60 * 1000
const MAX_TRACKED_LOGIN_KEYS = 10_000

type LoginFailures = Map<string, number[]>

function pruneLoginFailures(failures: LoginFailures, now: number): void {
  for (const [key, timestamps] of failures) {
    const recent = timestamps.filter((timestamp) => timestamp > now - LOGIN_FAILURE_WINDOW_MS)
    if (recent.length === 0) {
      failures.delete(key)
    } else {
      failures.set(key, recent)
    }
  }
}

function isLoginThrottled(failures: LoginFailures, key: string, now: number): boolean {
  const timestamps = failures.get(key)
  if (timestamps == null) {
    return false
  }

  const recent = timestamps.filter((timestamp) => timestamp > now - LOGIN_FAILURE_WINDOW_MS)
  if (recent.length === 0) {
    failures.delete(key)
    return false
  }
  failures.set(key, recent)
  return recent.length >= LOGIN_FAILURE_LIMIT
}

function recordLoginFailure(failures: LoginFailures, key: string, now: number): void {
  pruneLoginFailures(failures, now)
  if (!failures.has(key) && failures.size >= MAX_TRACKED_LOGIN_KEYS) {
    const oldestKey = failures.keys().next().value
    if (oldestKey != null) {
      failures.delete(oldestKey)
    }
  }

  const timestamps = failures.get(key) ?? []
  timestamps.push(now)
  failures.set(key, timestamps)
}

function hasLocalSetupHost(host: string | undefined): boolean {
  return host != null && (
    /^localhost(?::\d+)?$/i.test(host)
    || /^127\.0\.0\.1(?::\d+)?$/.test(host)
    || /^\[::1\](?::\d+)?$/i.test(host)
  )
}

export function registerAuthRoutes(
  app: Hono,
  deps: {
    authService: AuthService
    getClientAddress?: (c: Context) => string | undefined
    now?: () => Date
  },
): void {
  const { authService } = deps
  const getClientAddress = deps.getClientAddress ?? defaultGetClientAddress
  const now = deps.now ?? (() => new Date())
  const requireAuth = createBearerAuthMiddleware(authService)
  const failedUsernames: LoginFailures = new Map()
  const failedSources: LoginFailures = new Map()

  app.get("/api/v1/auth/status", (c) => c.json(authService.getSetupStatus()))

  app.post("/api/v1/auth/setup", async (c) => {
    // A loopback peer and local Host provide defense in depth. A same-host proxy can
    // still forge an upstream Host, so deployment must also deny this route outright.
    const host = c.req.header("host") ?? new URL(c.req.url).host
    if (!isLoopbackAddress(getClientAddress(c)) || !hasLocalSetupHost(host)) {
      return c.json({ error: "first-admin setup must be performed locally" }, 403)
    }

    const parsed = setupAdminRequestSchema.safeParse(await c.req.json())
    if (!parsed.success) {
      return c.json({ error: "invalid setup request" }, 400)
    }

    try {
      return c.json(await authService.setupAdmin(parsed.data), 201)
    } catch (error) {
      if (error instanceof BootstrapSecretInvalidError) {
        return c.json({ error: "invalid bootstrap secret" }, 403)
      }
      if (error instanceof ServerAlreadyClaimedError) {
        return c.json({ error: "server has already been claimed" }, 409)
      }
      throw error
    }
  })

  app.post("/api/v1/auth/login", async (c) => {
    const parsed = loginRequestSchema.safeParse(await c.req.json())
    if (!parsed.success) {
      return c.json({ error: "invalid login request" }, 400)
    }

    const requestTime = now().getTime()
    const usernameKey = parsed.data.username
    // getClientAddress reads the direct socket address. It deliberately ignores forwarded headers.
    // Without a direct source address, only the normalized-username limit is trustworthy.
    const sourceKey = getClientAddress(c)
    if (
      isLoginThrottled(failedUsernames, usernameKey, requestTime)
      || (sourceKey != null && isLoginThrottled(failedSources, sourceKey, requestTime))
    ) {
      return c.json({ error: "too many login attempts" }, 429)
    }

    try {
      return c.json(await authService.login(parsed.data))
    } catch (error) {
      if (error instanceof InvalidLoginError) {
        recordLoginFailure(failedUsernames, usernameKey, requestTime)
        if (sourceKey != null) {
          recordLoginFailure(failedSources, sourceKey, requestTime)
        }
        return c.json({ error: "invalid username or password" }, 401)
      }
      throw error
    }
  })

  app.get("/api/v1/auth/me", requireAuth, requireAccountAuth, (c) => {
    return c.json({ user: authService.getUser(getAuthenticatedUser(c).userId) })
  })

  app.post("/api/v1/auth/logout", requireAuth, requireAccountAuth, async (c) => {
    await authService.revokeCurrentCredential(getAuthenticatedUser(c).credentialId)
    return c.body(null, 204)
  })

  app.post("/api/v1/auth/registration-invites", requireAuth, requireAccountAuth, async (c) => {
    const parsed = registrationInviteRequestSchema.safeParse(await c.req.json())
    if (!parsed.success) {
      return c.json({ error: "invalid registration invitation request" }, 400)
    }

    try {
      const caller = getAuthenticatedUser(c)
      return c.json(await authService.createRegistrationInvite({
        actorUserId: caller.userId,
        actorCredentialId: caller.credentialId,
        requestId: randomUUID(),
        ttlMs: parsed.data.ttlMs,
      }), 201)
    } catch (error) {
      if (error instanceof NotAdminError) {
        return c.json({ error: "only an active admin may issue registration invitations" }, 403)
      }
      throw error
    }
  })

  app.post("/api/v1/auth/register", async (c) => {
    const parsed = registerRequestSchema.safeParse(await c.req.json())
    if (!parsed.success) {
      return c.json({ error: "invalid registration request" }, 400)
    }

    try {
      return c.json(await authService.register({ ...parsed.data, requestId: randomUUID() }), 201)
    } catch (error) {
      if (error instanceof UsernameConflictError) {
        return c.json({ error: "username is already registered" }, 409)
      }
      if (error instanceof RegistrationInviteInvalidError || error instanceof AuthServiceError) {
        return c.json({ error: "registration failed" }, 400)
      }
      throw error
    }
  })

  app.post("/api/v1/auth/enroll", requireAuth, requireAccountAuth, async (c) => {
    const parsed = enrollRequestSchema.safeParse(await c.req.json())
    if (!parsed.success) {
      return c.json({ error: "invalid enrollment request" }, 400)
    }

    try {
      return c.json({ user: await authService.enrollLegacy({
        userId: getAuthenticatedUser(c).userId,
        ...parsed.data,
      }) })
    } catch (error) {
      if (error instanceof LegacyEnrollmentError || error instanceof AuthServiceError) {
        return c.json({ error: "legacy enrollment failed" }, 400)
      }
      throw error
    }
  })

  app.post("/api/v1/auth/pairing-invites", (c) => c.json({ error: "pairing invitations retired" }, 410))
  app.post("/api/v1/auth/pairing-invites/redeem", (c) => c.json({ error: "pairing invitation redemption retired" }, 410))

  app.post("/api/v1/auth/credentials/:id/revoke", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      await authService.revokeCredential({ requesterUserId: userId, credentialId: c.req.param("id") })
      return c.body(null, 204)
    } catch (error) {
      if (error instanceof CredentialNotFoundError) {
        return c.json({ error: "credential not found" }, 404)
      }
      if (error instanceof Error && error.message.includes("only be revoked by its own user")) {
        return c.json({ error: "credential not found" }, 404)
      }
      throw error
    }
  })
}
