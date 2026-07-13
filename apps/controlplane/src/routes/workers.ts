import type { Hono } from "hono"

import type { DiscoveryWorkerClient } from "../services/worker-client.js"

export function registerWorkerRoutes(app: Hono, workerClient: DiscoveryWorkerClient): void {
  app.get("/api/v1/workers", async (c) => {
    const workers = await workerClient.listWorkers()
    return c.json({ workers })
  })
}
