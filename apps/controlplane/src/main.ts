import { serve } from "@hono/node-server"
import path from "node:path"

import { loadEnv } from "./config/env.js"
import { createAppDb } from "./persistence/db.js"
import { ensureUniqueInstanceOwnerIndex, inspectOwnerConflicts } from "./persistence/repositories/instances.js"
import { createServer } from "./server.js"
import { createAdminService } from "./services/admin-service.js"
import { createAuthService } from "./services/auth-service.js"
import { createAuditService } from "./services/audit-service.js"
import { createInstanceService } from "./services/instance-service.js"
import { runReconcilePass } from "./services/reconciler.js"
import { createSessionService } from "./services/session-service.js"
import { createTransportService } from "./services/transport-service.js"
import { LocalWorkerClient } from "./services/worker-client.js"

export function main(): void {
  const env = loadEnv()
  const workerClient = new LocalWorkerClient(env.workerBaseUrl)
  const db = createAppDb(env.databasePath)
  const ownerConflicts = inspectOwnerConflicts(db)
  if (ownerConflicts.length === 0) {
    ensureUniqueInstanceOwnerIndex(db)
  } else {
    console.error(`Instance owner conflicts require admin remediation: ${JSON.stringify(ownerConflicts)}`)
  }
  const auditService = createAuditService({ db })
  const instanceService = createInstanceService({
    db,
    workerClient,
    auditService,
  })
  const authService = createAuthService({ db, bootstrapSecretPath: env.bootstrapSecretPath, auditService })
  let transportService: ReturnType<typeof createTransportService> | undefined
  const sessionService = createSessionService({
    db,
    authService,
    defaultOwnerAbsenceTimeoutMs: env.ownerAbsenceTimeoutMs,
    ownerAbsenceTimeoutMinMs: env.ownerAbsenceTimeoutMinMs,
    ownerAbsenceTimeoutMaxMs: env.ownerAbsenceTimeoutMaxMs,
    destroyAttachmentTransport: async (input, audit) => {
      if (transportService == null) throw new Error("transport service is unavailable")
      await transportService.destroyAttachmentTransport({ ...input, audit })
    },
    auditService,
  })
  const adminService = createAdminService({ db, auditService })
  transportService = createTransportService({ instanceService, sessionService, workerClient, auditService })
  const baseUrl = `http://localhost:${env.port}`
  const adminDistPath = path.resolve(new URL(".", import.meta.url).pathname, "../../admin/dist")
  const app = createServer({
    workerClient,
    instanceService,
    sessionService,
    authService,
    adminService,
    transportService,
    auditService,
    db,
    baseUrl,
    adminDistPath,
    enableSessionDiagnostic: env.enableSessionDiagnostic,
  })

  setInterval(() => {
    void runReconcilePass({
      db,
      now: new Date(),
      inspectRunningInstance: async (instanceId) => {
        return await workerClient.inspectInstance(instanceId)
      },
      teardownPendingAttachment: async (input) => {
        if (transportService == null) throw new Error("transport service is unavailable")
        return await transportService.teardownPendingAttachment(input)
      },
      auditService,
    })
  }, env.reconcileIntervalMs)

  serve({
    fetch: app.fetch,
    port: env.port,
    hostname: env.listenHostname,
  })
}

main()
