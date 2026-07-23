import { existsSync } from "node:fs"
import path from "node:path"
import type { Hono } from "hono"
import { serveStatic } from "@hono/node-server/serve-static"

export function registerStaticRoutes(app: Hono, adminDistPath: string): void {
  if (!existsSync(adminDistPath)) {
    return
  }

  // Resolve relative paths for serveStatic (it expects paths relative to cwd)
  const relPath = path.relative(process.cwd(), adminDistPath)

  app.use("/admin/*", serveStatic({
    root: relPath,
    rewriteRequestPath: (p) => p.replace(/^\/admin/, ""),
  }))
  app.use("/admin", serveStatic({ path: path.join(relPath, "index.html") }))
}
