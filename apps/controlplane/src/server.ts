import { swaggerUI } from "@hono/swagger-ui"
import { Hono, type Context } from "hono"

import type { AppDb } from "./persistence/db.js"
import { registerAdminRoutes } from "./routes/admin.js"
import { registerAdminAuthRoutes, registerClientAuthRoutes, registerCommonAuthRoutes } from "./routes/auth.js"
import { registerInstanceRoutes } from "./routes/instances.js"
import { registerInviteRoutes } from "./routes/invites.js"
import { registerRootRoutes } from "./routes/root.js"
import { registerSessionDiagnosticRoutes, registerSessionRoutes } from "./routes/sessions.js"
import { registerSignalingRoutes } from "./routes/signaling.js"
import { registerStaticRoutes } from "./routes/static.js"
import { registerTemplateRoutes } from "./routes/templates.js"
import { registerWorkerRoutes } from "./routes/workers.js"
import type { AdminService } from "./services/admin-service.js"
import type { AuthService } from "./services/auth-service.js"
import { createAuditService, type AuditService } from "./services/audit-service.js"
import type { InstanceService } from "./services/instance-service.js"
import { createSessionInviteService } from "./services/session-invite-service.js"
import type { SessionService } from "./services/session-service.js"
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
    info: { title: "Mako Administration API", version: "0.1.0", description: "Loopback-only administration API." },
    paths: {
      "/healthz": { get: { summary: "Get control-plane health" } },
      "/api/v1/workers": { get: { summary: "List workers" } },
      "/api/v1/auth/status": { get: { summary: "Get first-admin setup status" } },
      "/api/v1/auth/setup": { post: { summary: "Set up the first administrator" } },
      "/api/v1/auth/login": { post: { summary: "Log in with an account username and password" } },
      "/api/v1/auth/me": { get: { summary: "Get the current account" } },
      "/api/v1/auth/logout": { post: { summary: "Revoke the current account credential" } },
      "/api/v1/auth/registration-invites": { post: { summary: "Create a registration invitation as an administrator" } },
      "/api/v1/admin/users": { get: { summary: "List accounts as an administrator" } },
      "/api/v1/admin/instances": { get: { summary: "List cross-owner instances as an administrator" } },
      "/api/v1/admin/audit": { get: { summary: "List audit events as an administrator" } },
    },
  }))
  app.get("/docs", swaggerUI({ url: "/openapi.json" }))
}

function resolveTransportService(deps: ServerDependencies): TransportService | undefined {
  if (deps.transportService != null) return deps.transportService
  if (deps.instanceService == null || deps.sessionService == null || !isManagedInstanceWorkerClient(deps.workerClient)) return undefined
  const auditService = deps.auditService ?? (deps.db == null ? undefined : createAuditService({ db: deps.db }))
  return createTransportService({ instanceService: deps.instanceService, sessionService: deps.sessionService, workerClient: deps.workerClient, auditService })
}

function registerSignaling(app: Hono, deps: ServerDependencies, transportService: TransportService | undefined): void {
  if (deps.instanceService == null || deps.sessionService == null || deps.authService == null || transportService == null || !isManagedInstanceWorkerClient(deps.workerClient)) return
  registerSignalingRoutes(app, {
    signalingService: createSignalingService(deps.workerClient),
    instanceService: deps.instanceService,
    sessionService: deps.sessionService,
    transportService,
    authService: deps.authService,
  })
}

/** Loopback-only WebUI and management API. */
export function createAdminServer(deps: ServerDependencies): Hono {
  const app = new Hono()
  const transportService = resolveTransportService(deps)
  if (deps.adminDistPath != null) registerStaticRoutes(app, deps.adminDistPath, deps.enableSessionDiagnostic)
  registerRootRoutes(app)
  registerWorkerRoutes(app, deps.workerClient)
  registerDocsRoutes(app)

  if (deps.authService != null) {
    registerCommonAuthRoutes(app, { authService: deps.authService, getClientAddress: deps.getClientAddress }, { sourceThrottle: true })
    registerAdminAuthRoutes(app, { authService: deps.authService, getClientAddress: deps.getClientAddress })
  }
  if (deps.authService != null && deps.adminService != null && deps.instanceService != null) {
    registerAdminRoutes(app, { authService: deps.authService, adminService: deps.adminService, instanceService: deps.instanceService, sessionService: deps.sessionService, transportService })
  }
  if (deps.enableSessionDiagnostic && deps.sessionService != null && deps.authService != null) {
    registerSessionDiagnosticRoutes(app, deps.sessionService, transportService, deps.authService)
    registerSignaling(app, deps, transportService)
  }
  return app
}

/** Public JSON-only API. Management and static routes are intentionally absent. */
export function createClientApiServer(deps: ServerDependencies): Hono {
  const app = new Hono()
  const transportService = resolveTransportService(deps)
  app.notFound((c) => c.json({ error: "not found" }, 404))

  if (deps.authService != null) {
    registerCommonAuthRoutes(app, { authService: deps.authService, getClientAddress: deps.getClientAddress }, { sourceThrottle: false })
    registerClientAuthRoutes(app, { authService: deps.authService, getClientAddress: deps.getClientAddress })
    registerTemplateRoutes(app, deps.workerClient, deps.authService)
  }
  if (deps.instanceService != null && deps.authService != null) registerInstanceRoutes(app, deps.instanceService, deps.authService)
  if (deps.sessionService != null && deps.authService != null) registerSessionRoutes(app, deps.sessionService, transportService, deps.authService)
  registerSignaling(app, deps, transportService)
  if (deps.sessionService != null && deps.authService != null && deps.db != null) {
    const auditService = deps.auditService ?? createAuditService({ db: deps.db })
    registerInviteRoutes(app, { sessionInviteService: createSessionInviteService({ db: deps.db, authService: deps.authService, auditService }), authService: deps.authService })
  }
  return app
}
