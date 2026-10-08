import { existsSync } from "node:fs"
import path from "node:path"
import type { Hono } from "hono"
import { serveStatic } from "@hono/node-server/serve-static"

export function registerStaticRoutes(app: Hono, adminDistPath: string, enableSessionDiagnostic = false): void {
  if (!existsSync(adminDistPath)) {
    return
  }

  if (!enableSessionDiagnostic) {
    app.get("/admin/session", (c) => c.notFound())
    app.get("/admin/session/*", (c) => c.notFound())
  }

  // Resolve relative paths for serveStatic (it expects paths relative to cwd)
  const relPath = path.relative(process.cwd(), adminDistPath)

  app.use("/admin/*", serveStatic({
    root: relPath,
    rewriteRequestPath: (p) => p.replace(/^\/admin/, ""),
  }))
  app.use("/admin", serveStatic({ path: path.join(relPath, "index.html") }))
  // SPA fallback: serve index.html for any /admin/* path that didn't match a static file
  app.get("/admin/*", serveStatic({ path: path.join(relPath, "index.html") }))
}
