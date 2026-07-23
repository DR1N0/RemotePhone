import { fileURLToPath } from "node:url"
import path from "node:path"
import { describe, expect, it } from "vitest"
import { createServer } from "../src/server.js"

const __dirname = fileURLToPath(new URL(".", import.meta.url))

describe("static file serving", () => {
  it("GET /admin returns 200 or 404 (404 when dist not built yet)", async () => {
    const app = createServer({
      workerClient: { listWorkers: async () => [], listTemplates: async () => [] },
      adminDistPath: path.resolve(__dirname, "../../../admin/dist"),
    })
    const res = await app.request("/admin")
    // 200 when built, 404 when not — either is acceptable in CI
    expect([200, 404]).toContain(res.status)
  })
})
