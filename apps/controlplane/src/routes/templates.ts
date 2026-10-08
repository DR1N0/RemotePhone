import type { Hono } from "hono"

import { createBearerAuthMiddleware, requireAccountAuth } from "../middleware/auth.js"
import type { AuthService } from "../services/auth-service.js"
import type { DiscoveryWorkerClient } from "../services/worker-client.js"

export function registerTemplateRoutes(app: Hono, workerClient: DiscoveryWorkerClient, authService: AuthService): void {
  app.get("/api/v1/templates", createBearerAuthMiddleware(authService), requireAccountAuth, async (c) => {
    const templates = await workerClient.listTemplates()
    return c.json({ templates })
  })
}
