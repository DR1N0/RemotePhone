import { randomUUID } from "node:crypto"

import type { Context, Hono } from "hono"

import { createInstanceRequestSchema } from "../domain/schemas.js"
import {
  createBearerAuthMiddleware,
  getAuthenticatedUser,
  requireAccountAuth,
} from "../middleware/auth.js"
import type { AuthService } from "../services/auth-service.js"
import { createAuditContext } from "../services/audit-service.js"
import {
  InstanceConflictError,
  InstanceForbiddenError,
  InstanceNotFoundError,
  InvalidRequestError,
  type InstanceService,
} from "../services/instance-service.js"

function errorStatus(error: unknown): 400 | 403 | 404 | 409 | 500 {
  if (error instanceof InvalidRequestError) {
    return 400
  }
  if (error instanceof InstanceConflictError) {
    return 409
  }
  if (error instanceof InstanceForbiddenError) {
    return 403
  }
  if (error instanceof InstanceNotFoundError) {
    return 404
  }
  return 500
}

function errorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message
  }
  return "unexpected error"
}

function jsonError(c: Context, error: unknown) {
  c.status(errorStatus(error))
  return c.json({ error: errorMessage(error) })
}

export function registerInstanceRoutes(app: Hono, instanceService: InstanceService, authService: AuthService): void {
  const requireAuth = createBearerAuthMiddleware(authService)

  app.post("/api/v1/instances", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      const input = createInstanceRequestSchema.parse(await c.req.json())
      const instance = await instanceService.createManagedInstance(input, userId)
      c.status(201)
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.get("/api/v1/instances", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      const instances = await instanceService.listManagedInstances(userId)
      return c.json({ instances })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.get("/api/v1/instances/:id", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      const instance = await instanceService.getManagedInstance(c.req.param("id"), userId)
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/instances/:id/start", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      const caller = getAuthenticatedUser(c)
      const instance = await instanceService.startManagedInstance(
        c.req.param("id"),
        userId,
        createAuditContext(caller.userId, caller.credentialId, randomUUID()),
      )
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/instances/:id/stop", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      const caller = getAuthenticatedUser(c)
      const instance = await instanceService.stopManagedInstance(
        c.req.param("id"),
        userId,
        createAuditContext(caller.userId, caller.credentialId, randomUUID()),
      )
      return c.json({ instance })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.delete("/api/v1/instances/:id", requireAuth, requireAccountAuth, async (c) => {
    const { userId } = getAuthenticatedUser(c)
    try {
      const caller = getAuthenticatedUser(c)
      await instanceService.deleteManagedInstance(
        c.req.param("id"),
        userId,
        createAuditContext(caller.userId, caller.credentialId, randomUUID()),
      )
      return c.body(null, 204)
    } catch (error) {
      return jsonError(c, error)
    }
  })
}
