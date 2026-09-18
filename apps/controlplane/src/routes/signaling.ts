import type { Context, Hono } from "hono"
import { z } from "zod"

import type { InstanceService } from "../services/instance-service.js"
import {
  AttachmentNotFoundError,
  SessionInvalidRequestError,
  SessionNotFoundError,
  type SessionService,
} from "../services/session-service.js"
import { InstanceNotFoundError } from "../services/instance-service.js"
import type { SignalingService } from "../services/signaling-service.js"
import {
  TransportUnavailableError,
  type TransportService,
} from "../services/transport-service.js"

const signalingSessionSchema = z.object({
  sessionId: z.string().min(1),
  attachmentId: z.string().min(1),
  sdpOffer: z.string().min(1),
})

const iceCandidateSchema = z.object({
  sessionId: z.string().min(1),
  attachmentId: z.string().min(1),
  candidate: z.string(),
})

function errorStatus(error: unknown): 400 | 404 | 500 | 502 {
  if (error instanceof TransportUnavailableError) return 502
  if (error instanceof SessionInvalidRequestError) return 400
  if (
    error instanceof SessionNotFoundError
    || error instanceof AttachmentNotFoundError
    || error instanceof InstanceNotFoundError
  ) return 404
  return 500
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "unexpected error"
}

function jsonError(c: Context, error: unknown) {
  c.status(errorStatus(error))
  return c.json({ error: errorMessage(error) })
}

export function registerSignalingRoutes(
  app: Hono,
  deps: {
    sessionService: SessionService
    instanceService: InstanceService
    signalingService: SignalingService
    transportService: TransportService
  },
): void {
  app.post("/api/v1/signaling/session", async (c) => {
    try {
      const input = signalingSessionSchema.parse(await c.req.json())
      const session = await deps.sessionService.getSession(input.sessionId)
      const attachment = session.attachments.find((a) => a.id === input.attachmentId)
      if (attachment == null) {
        throw new AttachmentNotFoundError(`attachment not found: ${input.attachmentId}`)
      }
      if (attachment.status !== "connected") {
        throw new SessionInvalidRequestError(`attachment is not connected: ${attachment.id}`)
      }
      const instance = await deps.instanceService.getManagedInstance(session.instanceId)
      if (instance.runtimeHandle == null) {
        throw new SessionInvalidRequestError(`instance is not running: ${instance.id}`)
      }
      await deps.transportService.ensureAttachmentTransport({
        sessionId: session.id,
        attachmentId: attachment.id,
      })
      const { sdpAnswer } = await deps.signalingService.relayOffer({
        instanceId: instance.id,
        attachmentId: attachment.id,
        sdpOffer: input.sdpOffer,
      })
      return c.json({ sdpAnswer })
    } catch (error) {
      return jsonError(c, error)
    }
  })

  app.post("/api/v1/signaling/ice-candidate", async (c) => {
    try {
      const input = iceCandidateSchema.parse(await c.req.json())
      const session = await deps.sessionService.getSession(input.sessionId)
      const attachment = session.attachments.find((a) => a.id === input.attachmentId)
      if (attachment == null) {
        throw new AttachmentNotFoundError(`attachment not found: ${input.attachmentId}`)
      }
      const instance = await deps.instanceService.getManagedInstance(session.instanceId)
      if (instance.runtimeHandle == null) {
        throw new SessionInvalidRequestError(`instance is not running: ${instance.id}`)
      }
      await deps.signalingService.relayICECandidate({
        instanceId: instance.id,
        attachmentId: attachment.id,
        candidate: input.candidate,
      })
      return c.json({ status: "ok" })
    } catch (error) {
      return jsonError(c, error)
    }
  })
}
