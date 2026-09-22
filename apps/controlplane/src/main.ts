import { serve } from "@hono/node-server"
import path from "node:path"

import { loadEnv } from "./config/env.js"
import { createAppDb } from "./persistence/db.js"
import { createServer } from "./server.js"
import { createAuthService } from "./services/auth-service.js"
import { createInstanceService } from "./services/instance-service.js"
import { runReconcilePass } from "./services/reconciler.js"
import { createSessionService } from "./services/session-service.js"
import { LocalWorkerClient } from "./services/worker-client.js"

export function main(): void {
  const env = loadEnv()
  const workerClient = new LocalWorkerClient(env.workerBaseUrl)
  const db = createAppDb(env.databasePath)
  const instanceService = createInstanceService({
    db,
    workerClient,
  })
  const sessionService = createSessionService({
    db,
    idleTtlMs: env.sessionIdleTtlMs,
  })
  const authService = createAuthService({ db })
  const baseUrl = `http://localhost:${env.port}`
  const adminDistPath = path.resolve(new URL(".", import.meta.url).pathname, "../../admin/dist")
  const app = createServer({
    workerClient,
    instanceService,
    sessionService,
    authService,
    db,
    baseUrl,
    adminDistPath,
  })

  setInterval(() => {
    void runReconcilePass({
      db,
      now: new Date(),
      stopManagedInstance: async (instanceId) => {
        await instanceService.stopManagedInstanceUnchecked(instanceId)
      },
      inspectRunningInstance: async (instanceId) => {
        return await workerClient.inspectInstance(instanceId)
      },
    })
  }, env.reconcileIntervalMs)

  serve({
    fetch: app.fetch,
    port: env.port,
  })
}

main()
