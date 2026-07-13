import type { Hono } from "hono"

export function registerRootRoutes(app: Hono): void {
  app.get("/", (c) => c.json({
    name: "mako-server",
    service: "control-plane",
  }))

  app.get("/healthz", (c) => c.json({ status: "ok" }))
}
