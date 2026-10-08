import { mkdtemp, rm, writeFile } from "node:fs/promises"
import { createServer } from "node:net"
import os from "node:os"
import path from "node:path"
import { afterEach, describe, expect, it, vi } from "vitest"
import { Hono } from "hono"

import { startListeners } from "../src/main.js"
import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAdminServer, createClientApiServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"

async function getPort(): Promise<number> {
  return await new Promise((resolve, reject) => {
    const server = createServer()
    server.once("error", reject)
    server.listen(0, "127.0.0.1", () => {
      const address = server.address()
      if (address == null || typeof address === "string") return reject(new Error("missing ephemeral address"))
      server.close((error) => error == null ? resolve(address.port) : reject(error))
    })
  })
}

describe("dual listener startup", () => {
  const stops: Array<() => Promise<void>> = []
  afterEach(async () => {
    await Promise.all(stops.splice(0).map(async (stop) => await stop()))
  })

  it("binds separate loopback ports and starts exactly one reconciler", async () => {
    const clientPort = await getPort()
    const adminPort = await getPort()
    const client = new Hono().get("/", (c) => c.json({ surface: "client" }))
    const admin = new Hono().get("/", (c) => c.json({ surface: "admin" }))
    const startReconciler = vi.fn(() => setInterval(() => {}, 60_000))
    const listeners = await startListeners({
      env: { port: clientPort, adminPort, listenHostname: "127.0.0.1" },
      clientFetch: client.fetch,
      adminFetch: admin.fetch,
      startReconciler,
    })
    stops.push(listeners.stop)
    expect(startReconciler).toHaveBeenCalledTimes(1)
    expect(await (await fetch(`http://127.0.0.1:${clientPort}/`)).json()).toEqual({ surface: "client" })
    expect(await (await fetch(`http://127.0.0.1:${adminPort}/`)).json()).toEqual({ surface: "admin" })
  })

  it("closes the client listener and does not start reconciliation when admin bind fails", async () => {
    const clientPort = await getPort()
    const adminPort = await getPort()
    const occupied = createServer().listen(adminPort, "127.0.0.1")
    const startReconciler = vi.fn(() => setInterval(() => {}, 60_000))
    const app = new Hono().get("/", (c) => c.json({ ok: true }))
    try {
      await expect(startListeners({
        env: { port: clientPort, adminPort, listenHostname: "127.0.0.1" },
        clientFetch: app.fetch,
        adminFetch: app.fetch,
        startReconciler,
      })).rejects.toThrow()
      expect(startReconciler).not.toHaveBeenCalled()
      await expect(fetch(`http://127.0.0.1:${clientPort}/`)).rejects.toThrow()
    } finally {
      await new Promise<void>((resolve, reject) => occupied.close((error) => error == null ? resolve() : reject(error)))
    }
  })

  it("keeps the built management UI on its own ephemeral loopback listener", async () => {
    resetTestDb()
    const clientPort = await getPort()
    const adminPort = await getPort()
    const adminDistPath = await mkdtemp(path.join(os.tmpdir(), "mako-admin-dist-"))
    await writeFile(path.join(adminDistPath, "index.html"), "<html><body>Management UI</body></html>")
    const db = createTestDb()
    const authService = createAuthService({ db })
    const workerClient = { listWorkers: async () => [], listTemplates: async () => [] }
    const deps = { db, authService, workerClient, adminDistPath, getClientAddress: () => "127.0.0.1" }
    const listeners = await startListeners({
      env: { port: clientPort, adminPort, listenHostname: "127.0.0.1" },
      clientFetch: createClientApiServer(deps).fetch,
      adminFetch: createAdminServer(deps).fetch,
      startReconciler: () => setInterval(() => {}, 60_000),
    })
    stops.push(listeners.stop)
    try {
      for (const route of ["/admin", "/docs", "/api/v1/auth/setup"]) {
        const response = await fetch(`http://127.0.0.1:${clientPort}${route}`)
        expect(response.status).toBe(404)
        expect(response.headers.get("content-type")).toContain("application/json")
      }
      const managementUi = await fetch(`http://127.0.0.1:${adminPort}/admin`)
      expect(managementUi.status).toBe(200)
      expect(managementUi.headers.get("content-type")).toContain("text/html")
      expect(await managementUi.text()).toContain("Management UI")
      expect((await fetch(`http://127.0.0.1:${adminPort}/api/v1/auth/status`)).status).toBe(200)
    } finally {
      await rm(adminDistPath, { recursive: true, force: true })
    }
  })
})
