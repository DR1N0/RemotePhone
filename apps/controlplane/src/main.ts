import { serve } from "@hono/node-server"
import path from "node:path"
import { pathToFileURL } from "node:url"
import type { Server } from "node:http"

import type { ControlPlaneEnv } from "./config/env.js"
import { loadEnv } from "./config/env.js"
import { createAppDb } from "./persistence/db.js"
import { ensureUniqueInstanceOwnerIndex, inspectOwnerConflicts } from "./persistence/repositories/instances.js"
import { createAdminServer, createClientApiServer } from "./server.js"
import { createAdminService } from "./services/admin-service.js"
import { createAuthService } from "./services/auth-service.js"
import { createAuditService } from "./services/audit-service.js"
import { createInstanceService } from "./services/instance-service.js"
import { runReconcilePass } from "./services/reconciler.js"
import { createSessionService } from "./services/session-service.js"
import { createTransportService } from "./services/transport-service.js"
import { LocalWorkerClient } from "./services/worker-client.js"

type ServeImplementation = typeof serve
type Listener = Pick<Server, "close" | "once" | "on" | "removeListener">

function closeListener(listener: Listener): Promise<void> {
  return new Promise((resolve, reject) => listener.close((error?: Error) => error == null ? resolve() : reject(error)))
}

function bindListener(
  serveImplementation: ServeImplementation,
  options: Parameters<ServeImplementation>[0],
): Promise<Listener> {
  return new Promise((resolve, reject) => {
    let listener: Listener
    try {
      listener = serveImplementation(options, () => {
        listener.removeListener("error", reject)
        resolve(listener)
      }) as unknown as Listener
      listener.once("error", reject)
    } catch (error) {
      reject(error)
    }
  })
}

export async function startListeners(input: {
  env: Pick<ControlPlaneEnv, "port" | "adminPort" | "listenHostname">
  clientFetch: Parameters<ServeImplementation>[0]["fetch"]
  adminFetch: Parameters<ServeImplementation>[0]["fetch"]
  serveImplementation?: ServeImplementation
  startReconciler: () => ReturnType<typeof setInterval>
}): Promise<{ clientListener: Listener; adminListener: Listener; stop: () => Promise<void> }> {
  const serveImplementation = input.serveImplementation ?? serve
  const clientListener = await bindListener(serveImplementation, {
    fetch: input.clientFetch,
    port: input.env.port,
    hostname: input.env.listenHostname,
  })
  let adminListener: Listener
  try {
    adminListener = await bindListener(serveImplementation, {
      fetch: input.adminFetch,
      port: input.env.adminPort,
      hostname: input.env.listenHostname,
    })
  } catch (error) {
    await closeListener(clientListener)
    throw error
  }

  // The reconciler has process-local transport coordination; exactly one starts only
  // after both loopback listeners are accepting connections.
  const reconciler = input.startReconciler()
  return {
    clientListener,
    adminListener,
    stop: async () => {
      clearInterval(reconciler)
      await Promise.all([closeListener(clientListener), closeListener(adminListener)])
    },
  }
}

export async function main(): Promise<void> {
  const env = loadEnv()
  const workerClient = new LocalWorkerClient(env.workerBaseUrl)
  const db = createAppDb(env.databasePath)
  const ownerConflicts = inspectOwnerConflicts(db)
  if (ownerConflicts.length === 0) ensureUniqueInstanceOwnerIndex(db)
  else console.error(`Instance owner conflicts require admin remediation: ${JSON.stringify(ownerConflicts)}`)

  const auditService = createAuditService({ db })
  const instanceService = createInstanceService({ db, workerClient, auditService })
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
  const adminDistPath = path.resolve(new URL(".", import.meta.url).pathname, "../../admin/dist")
  const commonDeps = {
    workerClient,
    instanceService,
    sessionService,
    authService,
    adminService,
    transportService,
    auditService,
    db,
    adminDistPath,
    enableSessionDiagnostic: env.enableSessionDiagnostic,
  }
  const clientApp = createClientApiServer(commonDeps)
  const adminApp = createAdminServer(commonDeps)

  await startListeners({
    env,
    clientFetch: clientApp.fetch,
    adminFetch: adminApp.fetch,
    startReconciler: () => setInterval(() => {
      void runReconcilePass({
        db,
        now: new Date(),
        inspectRunningInstance: async (instanceId) => await workerClient.inspectInstance(instanceId),
        teardownPendingAttachment: async (input) => {
          if (transportService == null) throw new Error("transport service is unavailable")
          return await transportService.teardownPendingAttachment(input)
        },
        auditService,
      })
    }, env.reconcileIntervalMs),
  })
}

const entrypoint = process.argv[1]
if (entrypoint != null && pathToFileURL(entrypoint).href === import.meta.url) {
  void main().catch((error: unknown) => {
    console.error(error)
    process.exitCode = 1
  })
}
