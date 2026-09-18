import { mkdtemp, rm, writeFile } from "node:fs/promises"
import os from "node:os"
import { fileURLToPath } from "node:url"
import path from "node:path"
import { describe, expect, it } from "vitest"
import { createServer } from "../src/server.js"

const __dirname = fileURLToPath(new URL(".", import.meta.url))

function createApp(adminDistPath: string) {
  return createServer({
    workerClient: { listWorkers: async () => [], listTemplates: async () => [] },
    adminDistPath,
  })
}

describe("static file serving", () => {
  it("GET /admin returns 200 or 404 (404 when dist not built yet)", async () => {
    const app = createApp(path.resolve(__dirname, "../../../admin/dist"))
    const res = await app.request("/admin")
    // 200 when built, 404 when not — either is acceptable in CI
    expect([200, 404]).toContain(res.status)
  })

  it("serves Admin assets before falling back to the SPA index for deep routes", async () => {
    const adminDistPath = await mkdtemp(path.join(os.tmpdir(), "mako-admin-dist-"))
    try {
      await writeFile(path.join(adminDistPath, "index.html"), "<html><body>Admin SPA</body></html>")
      await writeFile(path.join(adminDistPath, "app.js"), "console.log('asset')")
      const app = createApp(adminDistPath)

      const assetRes = await app.request("/admin/app.js")
      expect(assetRes.status).toBe(200)
      expect(await assetRes.text()).toBe("console.log('asset')")

      const routeRes = await app.request("/admin/session/session-1")
      expect(routeRes.status).toBe(200)
      expect(await routeRes.text()).toBe("<html><body>Admin SPA</body></html>")
    } finally {
      await rm(adminDistPath, { recursive: true, force: true })
    }
  })
})
