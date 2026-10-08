import { randomUUID } from "node:crypto"

import type { Context, Hono, MiddlewareHandler } from "hono"
import { z } from "zod"

import { setUserTypeRequestSchema } from "../domain/schemas.js"
import {
  createBearerAuthMiddleware,
  getAuthenticatedUser,
  requireAccountAuth,
} from "../middleware/auth.js"
import { InvalidAuditCursorError } from "../persistence/repositories/audit-events.js"
import {
  AccountCredentialNotFoundError,
  GuestAttachmentNotFoundError,
  LastActiveAdminError,
  NotAdminError,
  UserNotFoundError,
  type AdminService,
} from "../services/admin-service.js"
import type { AuthService } from "../services/auth-service.js"
import { createAuditContext, type AuditContext } from "../services/audit-service.js"
import type { SessionService } from "../services/session-service.js"
import { TransportUnavailableError, type TransportService } from "../services/transport-service.js"

export function registerAdminRoutes(app: Hono, deps: {
  adminService: AdminService
  authService: AuthService
  sessionService?: SessionService
  transportService?: TransportService
}): void {
  const requireAuth = createBearerAuthMiddleware(deps.authService)

  function mapAdminError(error: unknown, c: Context) {
    if (error instanceof NotAdminError) {
      return c.json({ error: "only an active admin may administer users" }, 403)
    }
    if (
      error instanceof UserNotFoundError
      || error instanceof GuestAttachmentNotFoundError
      || error instanceof AccountCredentialNotFoundError
    ) {
      return c.json({ error: error.message }, 404)
    }
    if (error instanceof InvalidAuditCursorError || error instanceof RangeError) {
      return c.json({ error: error.message }, 400)
    }
    if (error instanceof TransportUnavailableError) {
      return c.json({ error: error.message }, 502)
    }
    if (error instanceof LastActiveAdminError) {
      return c.json({ error: error.message }, 409)
    }
    throw error
  }

  const requireAdmin: MiddlewareHandler = async (c, next) => {
    try {
      deps.adminService.requireAdmin(getAuthenticatedUser(c).userId)
      await next()
    } catch (error) {
      return mapAdminError(error, c)
    }
  }

  function parsePaginationLimit(c: Context): number | null {
    const parsed = z.coerce.number().int().min(1).max(100).safeParse(c.req.query("limit") ?? "20")
    return parsed.success ? parsed.data : null
  }

  app.get("/api/v1/admin/users", requireAuth, requireAccountAuth, requireAdmin, (c) => {
    const limit = parsePaginationLimit(c)
    if (limit == null) return c.json({ error: "limit must be an integer from 1 to 100" }, 400)
    return c.json(deps.adminService.listUsers({
      actorUserId: getAuthenticatedUser(c).userId,
      limit,
      cursor: c.req.query("cursor") ?? null,
    }))
  })

  app.get("/api/v1/admin/users/:id/credentials", requireAuth, requireAccountAuth, requireAdmin, (c) => {
    try {
      return c.json({ items: deps.adminService.listCredentials({
        actorUserId: getAuthenticatedUser(c).userId,
        targetUserId: c.req.param("id"),
      }) })
    } catch (error) {
      return mapAdminError(error, c)
    }
  })

  app.post(
    "/api/v1/admin/users/:id/credentials/:credentialId/revoke",
    requireAuth,
    requireAccountAuth,
    requireAdmin,
    async (c) => {
      try {
        const caller = getAuthenticatedUser(c)
        await deps.adminService.revokeAccountCredential({
          actorUserId: caller.userId,
          actorCredentialId: caller.credentialId,
          requestId: randomUUID(),
          targetUserId: c.req.param("id"),
          credentialId: c.req.param("credentialId"),
        })
        return c.body(null, 204)
      } catch (error) {
        return mapAdminError(error, c)
      }
    },
  )

  app.get("/api/v1/admin/instances", requireAuth, requireAccountAuth, requireAdmin, (c) => {
    try {
      return c.json({ items: deps.adminService.listInstances({
        actorUserId: getAuthenticatedUser(c).userId,
      }) })
    } catch (error) {
      return mapAdminError(error, c)
    }
  })

  app.get("/api/v1/admin/audit", requireAuth, requireAccountAuth, requireAdmin, (c) => {
    const limit = parsePaginationLimit(c)
    if (limit == null) return c.json({ error: "limit must be an integer from 1 to 100" }, 400)
    try {
      return c.json(deps.adminService.listAudit({
        actorUserId: getAuthenticatedUser(c).userId,
        limit,
        cursor: c.req.query("cursor") ?? null,
      }))
    } catch (error) {
      return mapAdminError(error, c)
    }
  })

  app.post("/api/v1/admin/users/:id/type", requireAuth, requireAccountAuth, requireAdmin, async (c) => {
    const parsed = setUserTypeRequestSchema.safeParse(await c.req.json())
    if (!parsed.success) {
      return c.json({ error: "invalid user type request" }, 400)
    }
    try {
      const caller = getAuthenticatedUser(c)
      await deps.adminService.setUserType({
        actorUserId: caller.userId,
        actorCredentialId: caller.credentialId,
        requestId: randomUUID(),
        targetUserId: c.req.param("id") ?? "",
        type: parsed.data.type,
      })
      return c.body(null, 204)
    } catch (error) {
      return mapAdminError(error, c)
    }
  })

  async function teardownRevokedAttachment(
    input: { sessionId: string; attachmentId: string },
    audit: AuditContext,
  ): Promise<void> {
    if (deps.transportService == null || deps.sessionService == null) return
    await deps.transportService.teardownPendingAttachment({ ...input, audit })
  }

  async function setBanStatus(c: Context, status: "active" | "banned") {
    try {
      const caller = getAuthenticatedUser(c)
      const audit = createAuditContext(caller.userId, caller.credentialId, randomUUID())
      const affected = await deps.adminService.setBanStatus({
        ...audit,
        actorUserId: caller.userId,
        targetUserId: c.req.param("id") ?? "",
        status,
      })
      for (const attachment of affected) await teardownRevokedAttachment(attachment, audit)
      return c.body(null, 204)
    } catch (error) {
      return mapAdminError(error, c)
    }
  }

  app.post("/api/v1/admin/users/:id/ban", requireAuth, requireAccountAuth, requireAdmin, (c) => setBanStatus(c, "banned"))
  app.post("/api/v1/admin/users/:id/unban", requireAuth, requireAccountAuth, requireAdmin, (c) => setBanStatus(c, "active"))

  app.post(
    "/api/v1/admin/sessions/:id/attachments/:attachmentId/revoke",
    requireAuth,
    requireAccountAuth,
    requireAdmin,
    async (c) => {
      try {
        const caller = getAuthenticatedUser(c)
        const audit = createAuditContext(caller.userId, caller.credentialId, randomUUID())
        const attachment = await deps.adminService.revokeGuestAttachment({
          ...audit,
          actorUserId: caller.userId,
          sessionId: c.req.param("id"),
          attachmentId: c.req.param("attachmentId"),
        })
        await teardownRevokedAttachment(attachment, audit)
        return c.body(null, 204)
      } catch (error) {
        return mapAdminError(error, c)
      }
    },
  )
}
