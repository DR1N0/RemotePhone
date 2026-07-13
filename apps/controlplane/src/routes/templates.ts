import type { Hono } from "hono"

import type { DiscoveryWorkerClient } from "../services/worker-client.js"

export function registerTemplateRoutes(app: Hono, workerClient: DiscoveryWorkerClient): void {
  app.get("/api/v1/templates", async (c) => {
    const templates = await workerClient.listTemplates()
    return c.json({ templates })
  })
}
