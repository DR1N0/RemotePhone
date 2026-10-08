import { swaggerUI } from "@hono/swagger-ui"
import { Hono, type Context } from "hono"

import type { AppDb } from "./persistence/db.js"
import { registerAdminRoutes } from "./routes/admin.js"
import { registerAuthRoutes } from "./routes/auth.js"
import { registerInstanceRoutes } from "./routes/instances.js"
import { registerInviteRoutes } from "./routes/invites.js"
import { registerRootRoutes } from "./routes/root.js"
import { registerSessionRoutes } from "./routes/sessions.js"
import { registerSignalingRoutes } from "./routes/signaling.js"
import { registerStaticRoutes } from "./routes/static.js"
import { registerTemplateRoutes } from "./routes/templates.js"
import { registerWorkerRoutes } from "./routes/workers.js"
import type { AdminService } from "./services/admin-service.js"
import type { AuthService } from "./services/auth-service.js"
import { createAuditService, type AuditService } from "./services/audit-service.js"
import type { InstanceService } from "./services/instance-service.js"
import type { SessionService } from "./services/session-service.js"
import { createSessionInviteService } from "./services/session-invite-service.js"
import { createSignalingService } from "./services/signaling-service.js"
import { createTransportService, type TransportService } from "./services/transport-service.js"
import type { DiscoveryWorkerClient, ManagedInstanceWorkerClient } from "./services/worker-client.js"

export type ServerDependencies = {
  workerClient: DiscoveryWorkerClient
  instanceService?: InstanceService
  sessionService?: SessionService
  authService?: AuthService
  adminService?: AdminService
  transportService?: TransportService
  auditService?: AuditService
  getClientAddress?: (c: Context) => string | undefined
  db?: AppDb
  baseUrl?: string
  adminDistPath?: string
  enableSessionDiagnostic?: boolean
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
      description: "Raw HTTP binds to loopback only; publish this API through a protected HTTPS reverse proxy or tunnel.",
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
          description: "Requires an account-scoped bearer credential for the instance owner.",
        },
      },
      "/api/v1/sessions/{id}": {
        get: {
          summary: "Get a session",
        },
      },
      "/api/v1/sessions/{id}/attachments/{attachmentId}/resume": {
        post: {
          summary: "Resume an attachment with its bound credential",
        },
      },
      "/api/v1/sessions/{id}/attachments/{attachmentId}/credentials/refresh": {
        post: {
          summary: "Refresh an attachment credential before expiry",
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
      "/api/v1/auth/status": {
        get: {
          summary: "Get first-admin setup status",
          description: "Reports whether first-admin setup is permanently claimed.",
        },
      },
      "/api/v1/auth/setup": {
        post: {
          summary: "Set up the first administrator",
          description: "Local-server-only setup: requires a loopback caller and local Host plus the machine-local bootstrap secret before an administrator exists; later calls are denied. Public proxies must deny this route.",
        },
      },
      "/api/v1/auth/login": {
        post: {
          summary: "Log in with an account username and password",
          description: "Returns a fresh account-scoped bearer credential for an active non-guest user.",
        },
      },
      "/api/v1/auth/me": {
        get: {
          summary: "Get the current account",
        },
      },
      "/api/v1/auth/logout": {
        post: {
          summary: "Revoke the current account credential",
        },
      },
      "/api/v1/auth/register": {
        post: {
          summary: "Register an account with a registration invitation",
        },
      },
      "/api/v1/auth/enroll": {
        post: {
          summary: "Enroll a legacy passwordless account",
        },
      },
      "/api/v1/auth/registration-invites": {
        post: {
          summary: "Create a registration invitation as an administrator",
        },
      },
      "/api/v1/admin/users": {
        get: {
          summary: "List registered and administrator accounts as an administrator",
        },
      },
      "/api/v1/admin/users/{id}/credentials": {
        get: {
          summary: "List a user's account credentials as an administrator",
        },
      },
      "/api/v1/admin/users/{id}/credentials/{credentialId}/revoke": {
        post: {
          summary: "Revoke a user's account credential as an administrator",
        },
      },
      "/api/v1/admin/instances": {
        get: {
          summary: "List cross-owner instance operational metadata as an administrator",
        },
      },
      "/api/v1/admin/audit": {
        get: {
          summary: "List audit events as an administrator",
        },
      },
      "/api/v1/admin/users/{id}/type": {
        post: {
          summary: "Change a user's registered or administrator type",
        },
      },
      "/api/v1/admin/users/{id}/ban": {
        post: {
          summary: "Ban a user",
        },
      },
      "/api/v1/admin/users/{id}/unban": {
        post: {
          summary: "Unban a user",
        },
      },
      "/api/v1/admin/sessions/{id}/attachments/{attachmentId}/revoke": {
        post: {
          summary: "Revoke a guest attachment as an administrator",
        },
      },
    },
  }))

  app.get("/docs", swaggerUI({ url: "/openapi.json" }))
}

export function createServer(deps: ServerDependencies): Hono {
  const app = new Hono()
  const auditService = deps.auditService ?? (deps.db == null ? undefined : createAuditService({ db: deps.db }))

  if (deps.adminDistPath != null) {
    registerStaticRoutes(app, deps.adminDistPath, deps.enableSessionDiagnostic)
  }

  registerRootRoutes(app)
  registerWorkerRoutes(app, deps.workerClient)
  registerTemplateRoutes(app, deps.workerClient)
  registerDocsRoutes(app)

  if (deps.instanceService != null && deps.authService != null) {
    registerInstanceRoutes(app, deps.instanceService, deps.authService)
  }

  const transportService = deps.transportService ?? (
    deps.instanceService != null
      && deps.sessionService != null
      && isManagedInstanceWorkerClient(deps.workerClient)
      ? createTransportService({
        instanceService: deps.instanceService,
        sessionService: deps.sessionService,
        workerClient: deps.workerClient,
        auditService,
      })
      : undefined
  )

  if (deps.sessionService != null && deps.authService != null) {
    registerSessionRoutes(app, deps.sessionService, transportService, deps.authService)
  }

  if (
    deps.instanceService != null
    && deps.sessionService != null
    && deps.authService != null
    && transportService != null
    && isManagedInstanceWorkerClient(deps.workerClient)
  ) {
    const signalingService = createSignalingService(deps.workerClient)
    registerSignalingRoutes(app, {
      signalingService,
      instanceService: deps.instanceService,
      sessionService: deps.sessionService,
      transportService,
      authService: deps.authService,
    })
  }

  if (deps.sessionService != null && deps.authService != null && deps.db != null) {
    registerInviteRoutes(app, {
      sessionInviteService: createSessionInviteService({
        db: deps.db,
        authService: deps.authService,
        auditService,
      }),
      authService: deps.authService,
    })
  }

  if (deps.authService != null) {
    registerAuthRoutes(app, { authService: deps.authService, getClientAddress: deps.getClientAddress })
  }

  if (deps.authService != null && deps.adminService != null) {
    registerAdminRoutes(app, {
      authService: deps.authService,
      adminService: deps.adminService,
      sessionService: deps.sessionService,
      transportService,
    })
  }

  return app
}
