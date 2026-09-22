import type { Context, Hono } from "hono"

import { createSessionRequestSchema, joinSessionRequestSchema, resumeSessionRequestSchema } from "../domain/schemas.js"
import { createBearerAuthMiddleware, getAuthenticatedPrincipal } from "../middleware/auth.js"
import type { AuthService } from "../services/auth-service.js"
import {
  AttachmentNotFoundError,
  SessionConflictError,
  SessionForbiddenError,
  SessionInvalidRequestError,
  SessionNotFoundError,
  type SessionService,
} from "../services/session-service.js"
import {
  TransportUnavailableError,
  type TransportService,
} from "../services/transport-service.js"

function errorStatus(error: unknown): 400 | 403 | 404 | 409 | 500 | 502 {
  if (error instanceof TransportUnavailableError) {
    return 502
  }
  if (error instanceof SessionInvalidRequestError) {
    return 400
  }
  if (error instanceof SessionForbiddenError) {
    return 403
  }
  if (error instanceof SessionNotFoundError || error instanceof AttachmentNotFoundError) {
    return 404
  }
  if (error instanceof SessionConflictError) {
    return 409
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

export function registerSessionRoutes(
  app: Hono,
  sessionService: SessionService,
  transportService: TransportService | undefined,
  authService: AuthService,
): void {
  const requireAuth = createBearerAuthMiddleware(authService)

  async function getOptionalPrincipalId(c: Context): Promise<string | undefined> {
    const header = c.req.header("Authorization")
    if (!header?.startsWith("Bearer ")) {
      return undefined
    }
    const principal = await authService.authenticate(header.slice("Bearer ".length))
    return principal?.principalId
  }

  app.post("/api/v1/sessions", requireAuth, async (c) => {
    const { principalId } = getAuthenticatedPrincipal(c)
    try {
      const input = createSessionRequestSchema.parse(await c.req.json())
      const session = await sessionService.createSession({ ...input, requesterPrincipalId: principalId })
      c.status(201)
      return c.json({ session })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/join", async (c) => {
    try {
      const input = joinSessionRequestSchema.parse(await c.req.json())
      const attachment = await sessionService.joinSession({
        sessionId: c.req.param("id"),
        clientId: input.clientId,
      })
      c.status(201)
      return c.json({ attachment })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/resume", async (c) => {
    try {
      const input = resumeSessionRequestSchema.parse(await c.req.json())
      const attachment = await sessionService.resumeAttachment({
        sessionId: c.req.param("id"),
        reconnectToken: input.reconnectToken,
      })
      return c.json({ attachment })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/disconnect", async (c) => {
    let reconnectToken: string | undefined
    try {
      const body = await c.req.json() as { reconnectToken?: string }
      reconnectToken = body.reconnectToken
    } catch {
      reconnectToken = undefined
    }
    const requesterPrincipalId = await getOptionalPrincipalId(c)

    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const session = await sessionService.disconnectAttachment({
        sessionId,
        attachmentId,
        requesterPrincipalId,
        requesterReconnectToken: reconnectToken,
      })
      if (transportService != null) {
        await transportService.destroyAttachmentTransport({ sessionId, attachmentId })
      }
      return c.json({ session })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/promote", requireAuth, async (c) => {
    const { principalId } = getAuthenticatedPrincipal(c)
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const attachment = await sessionService.promoteAttachment({ sessionId, attachmentId, requesterPrincipalId: principalId })
      if (transportService != null) {
        try {
          await transportService.syncAttachmentRole({
            sessionId,
            attachmentId,
            role: attachment.role,
          })
          await sessionService.setAttachmentPendingTransportRole({
            sessionId,
            attachmentId,
            targetRole: null,
          })
        } catch (error) {
          await sessionService.setAttachmentPendingTransportRole({
            sessionId,
            attachmentId,
            targetRole: "participant",
          })
          try {
            await transportService.syncAttachmentRole({
              sessionId,
              attachmentId,
              role: "observer",
            })
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

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/demote", requireAuth, async (c) => {
    const { principalId } = getAuthenticatedPrincipal(c)
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      if (transportService != null) {
        try {
          await transportService.syncAttachmentRole({
            sessionId,
            attachmentId,
            role: "observer",
          })
        } catch (error) {
          await sessionService.setAttachmentPendingTransportRole({
            sessionId,
            attachmentId,
            targetRole: "observer",
          })
          throw error
        }
      }
      try {
        const attachment = await sessionService.demoteAttachment({ sessionId, attachmentId, requesterPrincipalId: principalId })
        if (transportService != null) {
          await sessionService.setAttachmentPendingTransportRole({
            sessionId,
            attachmentId,
            targetRole: null,
          })
        }
        return c.json({ attachment })
      } catch (error) {
        if (transportService != null) {
          await sessionService.setAttachmentPendingTransportRole({
            sessionId,
            attachmentId,
            targetRole: "observer",
          })
        }
        throw error
      }
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.get("/api/v1/sessions/:id", async (c) => {
    try {
      const session = await sessionService.getSession(c.req.param("id"))
      return c.json({ session })
    } catch (error) {
      return jsonError(c, error)
    }
  })
}
