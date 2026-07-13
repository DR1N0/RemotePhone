import type { Context, Hono } from "hono"

import { createSignalingSessionRequestSchema } from "../domain/schemas.js"
import {
  InstanceNotFoundError,
  type InstanceService,
} from "../services/instance-service.js"
import {
  AttachmentNotFoundError,
  SessionInvalidRequestError,
  SessionNotFoundError,
  type SessionService,
} from "../services/session-service.js"
import type { ManagedInstanceWorkerClient } from "../services/worker-client.js"

function errorStatus(error: unknown): 400 | 404 | 500 {
  if (error instanceof SessionInvalidRequestError) {
    return 400
  }
  if (
    error instanceof SessionNotFoundError
    || error instanceof AttachmentNotFoundError
    || error instanceof InstanceNotFoundError
  ) {
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

export function registerSignalingRoutes(
  app: Hono,
  deps: {
    sessionService: SessionService
    instanceService: InstanceService
    workerClient: ManagedInstanceWorkerClient
  },
): void {
  app.post("/api/v1/signaling/session", async (c) => {
    try {
      const input = createSignalingSessionRequestSchema.parse(await c.req.json())
      const session = await deps.sessionService.getSession(input.sessionId)
      const attachment = session.attachments.find((candidate) => candidate.id === input.attachmentId)
      if (attachment == null) {
        throw new AttachmentNotFoundError(`attachment not found: ${input.attachmentId}`)
      }

      const instance = await deps.instanceService.getManagedInstance(session.instanceId)
      if (instance.runtimeHandle == null) {
        throw new SessionInvalidRequestError(`managed instance is not started: ${instance.id}`)
      }

      const transport = await deps.workerClient.createTransportSession({
        sessionId: session.id,
        attachmentId: attachment.id,
        instanceId: instance.id,
        runtimeClass: instance.runtimeClass,
        runtimeHandle: instance.runtimeHandle,
      })

      return c.json({ transport })
    } catch (error) {
      return jsonError(c, error)
    }
  })
}
