import { swaggerUI } from "@hono/swagger-ui"
import { Hono } from "hono"

import type { AppDb } from "./persistence/db.js"
import { registerInstanceRoutes } from "./routes/instances.js"
import { registerInviteRoutes } from "./routes/invites.js"
import { registerRootRoutes } from "./routes/root.js"
import { registerSessionRoutes } from "./routes/sessions.js"
import { registerSignalingRoutes } from "./routes/signaling.js"
import { registerTemplateRoutes } from "./routes/templates.js"
import { registerWorkerRoutes } from "./routes/workers.js"
import type { InstanceService } from "./services/instance-service.js"
import type { SessionService } from "./services/session-service.js"
import { createSignalingService } from "./services/signaling-service.js"
import type { DiscoveryWorkerClient, ManagedInstanceWorkerClient } from "./services/worker-client.js"

export type ServerDependencies = {
  workerClient: DiscoveryWorkerClient
  instanceService?: InstanceService
  sessionService?: SessionService
  db?: AppDb
  baseUrl?: string
}

function registerDocsRoutes(app: Hono): void {
  app.get("/openapi.json", (c) => c.json({
    openapi: "3.1.0",
    info: {
      title: "Mako Server API",
      version: "0.1.0",
    },
    paths: {
      "/": {
        get: {
          summary: "Get control-plane metadata",
        },
      },
      "/healthz": {
        get: {
          summary: "Get control-plane health",
        },
      },
      "/api/v1/workers": {
        get: {
          summary: "List workers",
        },
      },
      "/api/v1/templates": {
        get: {
          summary: "List runtime templates",
        },
      },
      "/api/v1/instances": {
        get: {
          summary: "List managed instances",
        },
        post: {
          summary: "Create a managed instance",
        },
      },
      "/api/v1/instances/{id}": {
        get: {
          summary: "Get a managed instance",
        },
        delete: {
          summary: "Delete a managed instance",
        },
      },
      "/api/v1/instances/{id}/start": {
        post: {
          summary: "Start a managed instance",
        },
      },
      "/api/v1/instances/{id}/stop": {
        post: {
          summary: "Stop a managed instance",
        },
      },
      "/api/v1/sessions": {
        post: {
          summary: "Create a session",
        },
      },
      "/api/v1/sessions/{id}": {
        get: {
          summary: "Get a session",
        },
      },
      "/api/v1/sessions/{id}/join": {
        post: {
          summary: "Join a session as a new attachment",
        },
      },
      "/api/v1/sessions/{id}/resume": {
        post: {
          summary: "Resume a disconnected session attachment",
        },
      },
      "/api/v1/sessions/{id}/attachments/{attachmentId}/disconnect": {
        post: {
          summary: "Disconnect a session attachment",
        },
      },
      "/api/v1/sessions/{id}/attachments/{attachmentId}/promote": {
        post: {
          summary: "Promote an attachment to participant",
        },
      },
      "/api/v1/sessions/{id}/attachments/{attachmentId}/demote": {
        post: {
          summary: "Demote an attachment to observer",
        },
      },
      "/api/v1/signaling/session": {
        post: {
          summary: "Request prototype session transport metadata",
        },
      },
    },
  }))

  app.get("/docs", swaggerUI({ url: "/openapi.json" }))
}

export function createServer(deps: ServerDependencies): Hono {
  const app = new Hono()

  registerRootRoutes(app)
  registerWorkerRoutes(app, deps.workerClient)
  registerTemplateRoutes(app, deps.workerClient)
  registerDocsRoutes(app)

  if (deps.instanceService != null) {
    registerInstanceRoutes(app, deps.instanceService)
  }

  if (deps.sessionService != null) {
    registerSessionRoutes(app, deps.sessionService)
  }

  if (deps.instanceService != null && deps.sessionService != null && "forwardOffer" in deps.workerClient) {
    const signalingService = createSignalingService(deps.workerClient as ManagedInstanceWorkerClient)
    registerSignalingRoutes(app, {
      signalingService,
      instanceService: deps.instanceService,
      sessionService: deps.sessionService,
    })
  }

  if (deps.sessionService != null && deps.db != null) {
    registerInviteRoutes(app, {
      db: deps.db,
      sessionService: deps.sessionService,
      baseUrl: deps.baseUrl ?? "http://localhost:3000",
    })
  }

  return app
}
