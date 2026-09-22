import { swaggerUI } from "@hono/swagger-ui"
import { Hono, type Context } from "hono"

import type { AppDb } from "./persistence/db.js"
import { registerAuthRoutes } from "./routes/auth.js"
import { registerInstanceRoutes } from "./routes/instances.js"
import { registerInviteRoutes } from "./routes/invites.js"
import { registerRootRoutes } from "./routes/root.js"
import { registerSessionRoutes } from "./routes/sessions.js"
import { registerSignalingRoutes } from "./routes/signaling.js"
import { registerStaticRoutes } from "./routes/static.js"
import { registerTemplateRoutes } from "./routes/templates.js"
import { registerWorkerRoutes } from "./routes/workers.js"
import type { AuthService } from "./services/auth-service.js"
import type { InstanceService } from "./services/instance-service.js"
import type { SessionService } from "./services/session-service.js"
import { createSignalingService } from "./services/signaling-service.js"
import { createTransportService } from "./services/transport-service.js"
import type { DiscoveryWorkerClient, ManagedInstanceWorkerClient } from "./services/worker-client.js"

export type ServerDependencies = {
  workerClient: DiscoveryWorkerClient
  instanceService?: InstanceService
  sessionService?: SessionService
  authService?: AuthService
  getClientAddress?: (c: Context) => string | undefined
  db?: AppDb
  baseUrl?: string
  adminDistPath?: string
}

function isManagedInstanceWorkerClient(workerClient: DiscoveryWorkerClient): workerClient is ManagedInstanceWorkerClient {
  return "startInstance" in workerClient
    && "inspectInstance" in workerClient
    && "stopInstance" in workerClient
    && "createTransportSession" in workerClient
    && "updateTransportRole" in workerClient
    && "destroyTransportSession" in workerClient
    && "forwardOffer" in workerClient
    && "forwardICECandidate" in workerClient
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

  if (deps.adminDistPath != null) {
    registerStaticRoutes(app, deps.adminDistPath)
  }

  registerRootRoutes(app)
  registerWorkerRoutes(app, deps.workerClient)
  registerTemplateRoutes(app, deps.workerClient)
  registerDocsRoutes(app)

  if (deps.instanceService != null && deps.authService != null) {
    registerInstanceRoutes(app, deps.instanceService, deps.authService)
  }

  const transportService = deps.instanceService != null
    && deps.sessionService != null
    && isManagedInstanceWorkerClient(deps.workerClient)
    ? createTransportService({
      instanceService: deps.instanceService,
      sessionService: deps.sessionService,
      workerClient: deps.workerClient,
    })
    : undefined

  if (deps.sessionService != null) {
    registerSessionRoutes(app, deps.sessionService, transportService)
  }

  if (
    deps.instanceService != null
    && deps.sessionService != null
    && transportService != null
    && isManagedInstanceWorkerClient(deps.workerClient)
  ) {
    const signalingService = createSignalingService(deps.workerClient)
    registerSignalingRoutes(app, {
      signalingService,
      instanceService: deps.instanceService,
      sessionService: deps.sessionService,
      transportService,
    })
  }

  if (deps.sessionService != null && deps.db != null) {
    registerInviteRoutes(app, {
      db: deps.db,
      sessionService: deps.sessionService,
      baseUrl: deps.baseUrl ?? "http://localhost:3000",
    })
  }

  if (deps.authService != null) {
    registerAuthRoutes(app, { authService: deps.authService, getClientAddress: deps.getClientAddress })
  }

  return app
}
