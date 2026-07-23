import { randomUUID } from "node:crypto"
import type { Hono } from "hono"

import {
  createAttachment,
  getAttachmentByInviteToken,
  setAttachmentInviteToken,
} from "../persistence/repositories/attachments.js"
import type { AppDb } from "../persistence/db.js"
import {
  SessionNotFoundError,
  type SessionService,
} from "../services/session-service.js"

export function registerInviteRoutes(
  app: Hono,
  deps: {
    db: AppDb
    sessionService: SessionService
    baseUrl: string
  },
): void {
  // Create a single-use invite token for a session
  app.post("/api/v1/sessions/:id/invites", async (c) => {
    const sessionId = c.req.param("id")
    try {
      await deps.sessionService.getSession(sessionId)
    } catch (e) {
      if (e instanceof SessionNotFoundError) {
        return c.json({ error: "session not found" }, 404)
      }
      return c.json({ error: "unexpected error" }, 500)
    }

    // Create a disconnected observer attachment with an invite token
    const attachmentId = randomUUID()
    const token = randomUUID()
    createAttachment(deps.db, {
      id: attachmentId,
      sessionId,
      clientId: `invite-${attachmentId}`,
      role: "observer",
      status: "disconnected",
      reconnectToken: null,
      inviteToken: token,
    })

    const inviteUrl = `${deps.baseUrl}/session/${sessionId}?token=${token}`
    return c.json({ token, inviteUrl, attachmentId }, 201)
  })

  // Redeem an invite token — activate the attachment and redirect the browser
  app.get("/session/:id", async (c) => {
    const token = c.req.query("token")
    const attachmentId = c.req.query("attachmentId")

    // If attachmentId already present, redirect to admin page
    if (attachmentId != null) {
      return c.redirect(`/admin/session.html?sessionId=${c.req.param("id")}&attachmentId=${attachmentId}`)
    }

    if (token == null) {
      return c.json({ error: "token or attachmentId required" }, 400)
    }

    const row = getAttachmentByInviteToken(deps.db, token)
    if (row == null || row.sessionId !== c.req.param("id")) {
      return c.json({ error: "invalid or expired token" }, 400)
    }

    // Consume the token and activate the attachment
    setAttachmentInviteToken(deps.db, { id: row.id, inviteToken: null })

    return c.redirect(
      `/session/${row.sessionId}?attachmentId=${row.id}`,
      302,
    )
  })
}
