import type { Context, Hono } from "hono"

import { createSessionRequestSchema, joinSessionRequestSchema, resumeSessionRequestSchema } from "../domain/schemas.js"
import {
  AttachmentNotFoundError,
  SessionConflictError,
  SessionInvalidRequestError,
  SessionNotFoundError,
  type SessionService,
} from "../services/session-service.js"
import {
  TransportUnavailableError,
  type TransportService,
} from "../services/transport-service.js"

function errorStatus(error: unknown): 400 | 404 | 409 | 500 | 502 {
  if (error instanceof TransportUnavailableError) {
    return 502
  }
  if (error instanceof SessionInvalidRequestError) {
    return 400
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
  transportService?: TransportService,
): void {
  app.post("/api/v1/sessions", async (c) => {
    try {
      const input = createSessionRequestSchema.parse(await c.req.json())
      const session = await sessionService.createSession(input)
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
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const session = await sessionService.disconnectAttachment({ sessionId, attachmentId })
      if (transportService != null) {
        await transportService.destroyAttachmentTransport({ sessionId, attachmentId })
      }
      return c.json({ session })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/promote", async (c) => {
    try {
      const sessionId = c.req.param("id")
      const attachmentId = c.req.param("attachmentId")
      const attachment = await sessionService.promoteAttachment({ sessionId, attachmentId })
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

  app.post("/api/v1/sessions/:id/attachments/:attachmentId/demote", async (c) => {
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
        const attachment = await sessionService.demoteAttachment({ sessionId, attachmentId })
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
