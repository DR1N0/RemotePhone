import { randomUUID } from "node:crypto"

import type { Context, Hono } from "hono"

import { createSessionRequestSchema } from "../domain/schemas.js"
import {
  createBearerAuthMiddleware,
  getAuthenticatedUser,
  requireAccountAuth,
  requireAttachmentAuth,
} from "../middleware/auth.js"
import type { AuthService } from "../services/auth-service.js"
import { createAuditContext } from "../services/audit-service.js"
import {
  AttachmentNotFoundError,
  SessionConflictError,
  SessionForbiddenError,
  SessionInvalidRequestError,
  SessionNotFoundError,
  type SessionService,
} from "../services/session-service.js"
import { TransportUnavailableError, type TransportService } from "../services/transport-service.js"

function errorStatus(error: unknown): 400 | 403 | 404 | 409 | 500 | 502 {
  if (error instanceof TransportUnavailableError) return 502
  if (error instanceof SessionInvalidRequestError) return 400
  if (error instanceof SessionForbiddenError) return 403
  if (error instanceof SessionNotFoundError || error instanceof AttachmentNotFoundError) return 404
  if (error instanceof SessionConflictError) return 409
  return 500
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "unexpected error"
}

function jsonError(c: Context, error: unknown) {
  c.status(errorStatus(error))
  return c.json({ error: errorMessage(error) })
}

export function registerSessionRoutes(
  app: Hono,
  sessionService: SessionService,
  transportService: TransportService | undefined,
  authService: AuthService,
): void {
  const requireAuth = createBearerAuthMiddleware(authService)

  app.post("/api/v1/sessions", requireAuth, requireAccountAuth, async (c) => {
    try {
      const input = createSessionRequestSchema.parse(await c.req.json())
      const created = await sessionService.createSession({ ...input, caller: getAuthenticatedUser(c) })
      c.status(201)
      return c.json(created)
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/join", (c) => {
    return c.json({ error: "session join is retired; redeem a session invite" }, 410)
  })

  app.post("/api/v1/sessions/:id/resume", (c) => {
    return c.json({ error: "session resume requires an attachment credential" }, 410)
  })

  app.post(
    "/api/v1/sessions/:id/attachments/:attachmentId/resume",
    requireAuth,
    requireAttachmentAuth,
    async (c) => {
      try {
        const result = await sessionService.resumeAttachment({
          sessionId: c.req.param("id"),
          attachmentId: c.req.param("attachmentId"),
          caller: getAuthenticatedUser(c),
        })
        return c.json(result)
      } catch (error) {
        return jsonError(c, error)
      }
    },
  )

  app.post(
    "/api/v1/sessions/:id/attachments/:attachmentId/credentials/refresh",
    requireAuth,
    requireAttachmentAuth,
    async (c) => {
      try {
        const result = await sessionService.refreshAttachmentCredential({
          sessionId: c.req.param("id"),
          attachmentId: c.req.param("attachmentId"),
          caller: getAuthenticatedUser(c),
        })
        return c.json(result)
      } catch (error) {
        return jsonError(c, error)
      }
    },
  )

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/disconnect", requireAuth, async (c) => {
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const caller = getAuthenticatedUser(c)
      const audit = createAuditContext(caller.userId, caller.credentialId, randomUUID())
      const session = await sessionService.disconnectAttachment({
        sessionId,
        attachmentId,
        caller,
        requestId: audit.requestId,
      })
      if (transportService != null) {
        await transportService.teardownPendingAttachment({ sessionId, attachmentId, audit })
      }
      return c.json({ session })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/promote", requireAuth, requireAccountAuth, async (c) => {
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const caller = getAuthenticatedUser(c)
      const audit = createAuditContext(caller.userId, caller.credentialId, randomUUID())
      const attachment = await sessionService.promoteAttachment({
        sessionId,
        attachmentId,
        caller,
        requestId: audit.requestId,
      })
      if (transportService != null) {
        try {
          await transportService.syncAttachmentRole({ sessionId, attachmentId, role: attachment.role, audit })
          await sessionService.setAttachmentPendingTransportRole({ sessionId, attachmentId, targetRole: null })
        } catch (error) {
          await sessionService.setAttachmentPendingTransportRole({ sessionId, attachmentId, targetRole: "participant" })
          try {
            await transportService.syncAttachmentRole({ sessionId, attachmentId, role: "observer", audit })
          } catch {
            // A later ensure uses the durable restrictive pending role.
          }
          throw error
        }
      }
      return c.json({ attachment })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/demote", requireAuth, requireAccountAuth, async (c) => {
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const caller = getAuthenticatedUser(c)
      const audit = createAuditContext(caller.userId, caller.credentialId, randomUUID())
      await sessionService.authorizeSessionModeration(sessionId, caller)
      if (transportService != null) {
        try {
          await transportService.syncAttachmentRole({ sessionId, attachmentId, role: "observer", audit })
        } catch (error) {
          await sessionService.setAttachmentPendingTransportRole({ sessionId, attachmentId, targetRole: "observer" })
          throw error
        }
      }
      try {
        const attachment = await sessionService.demoteAttachment({
          sessionId,
          attachmentId,
          caller,
          requestId: audit.requestId,
        })
        if (transportService != null) await sessionService.setAttachmentPendingTransportRole({ sessionId, attachmentId, targetRole: null })
        return c.json({ attachment })
      } catch (error) {
        if (transportService != null) {
          await sessionService.setAttachmentPendingTransportRole({ sessionId, attachmentId, targetRole: "observer" })
        }
        throw error
      }
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.get("/api/v1/sessions/:id", requireAuth, async (c) => {
    try {
      const session = await sessionService.getSession(c.req.param("id"), getAuthenticatedUser(c))
      return c.json({ session })
    } catch (error) {
      // Session reads conceal private existence from valid but unrelated accounts.
      if (error instanceof SessionForbiddenError) {
        return c.json({ error: `session not found: ${c.req.param("id")}` }, 404)
      }
      return jsonError(c, error)
    }
  })
}
