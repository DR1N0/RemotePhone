import { describe, expect, it, vi } from "vitest"

import { createAdminServer, createClientApiServer } from "../src/server.js"
import { createTestDb } from "../src/persistence/db.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createAdminAuthFixture, setupAdminInService } from "./helpers/admin-auth.js"

describe("control-plane discovery routes", () => {
  it("returns control-plane metadata at the root endpoint", async () => {
    const app = createAdminServer({
      workerClient: {
        listWorkers: vi.fn().mockResolvedValue([]),
        listTemplates: vi.fn().mockResolvedValue([]),
      },
    })

    const res = await app.request("/")

    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({
      name: "mako-server",
      service: "control-plane",
    })
  })

  it("returns ok from the health endpoint", async () => {
    const app = createAdminServer({
      workerClient: {
        listWorkers: vi.fn().mockResolvedValue([]),
        listTemplates: vi.fn().mockResolvedValue([]),
      },
    })

    const res = await app.request("/healthz")

    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({ status: "ok" })
  })

  it("lists workers from the worker client", async () => {
    const workerClient = {
      listWorkers: vi.fn().mockResolvedValue([
        {
          id: "local",
          kind: "local",
          hostOs: "macos",
          arch: "arm64",
          health: "ok",
          installedRuntimes: [
            {
              runtimeClass: "android-studio-emulator",
              displayName: "Android Studio Emulator",
            },
          ],
        },
      ]),
      listTemplates: vi.fn().mockResolvedValue([]),
    }
    const app = createAdminServer({ workerClient })

    const res = await app.request("/api/v1/workers")

    expect(res.status).toBe(200)
    expect(await res.json()).toEqual({
      workers: [
        {
          id: "local",
          kind: "local",
          hostOs: "macos",
          arch: "arm64",
          health: "ok",
          installedRuntimes: [
            {
              runtimeClass: "android-studio-emulator",
              displayName: "Android Studio Emulator",
            },
          ],
        },
      ],
    })
    expect(workerClient.listWorkers).toHaveBeenCalledTimes(1)
    expect(workerClient.listTemplates).not.toHaveBeenCalled()
  })

  it("lists templates from the worker client", async () => {
    const workerClient = {
      listWorkers: vi.fn().mockResolvedValue([]),
      listTemplates: vi.fn().mockResolvedValue([
        {
          templateId: "macos-android-studio:Medium_Phone",
          runtimeClass: "android-studio-emulator",
          displayName: "Medium Phone",
          metadata: {
            avdName: "Medium_Phone",
          },
        },
      ]),
    }
    const db = createTestDb()
    const fixture = createAdminAuthFixture()
    try {
      const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
      const app = createClientApiServer({ workerClient, authService })
      expect((await app.request("/api/v1/templates")).status).toBe(401)
      const { token } = await setupAdminInService(authService, fixture)
      const res = await app.request("/api/v1/templates", { headers: { Authorization: `Bearer ${token}` } })

      expect(res.status).toBe(200)
    expect(await res.json()).toEqual({
      templates: [
        {
          templateId: "macos-android-studio:Medium_Phone",
          runtimeClass: "android-studio-emulator",
          displayName: "Medium Phone",
          metadata: {
            avdName: "Medium_Phone",
          },
        },
      ],
    })
      expect(workerClient.listTemplates).toHaveBeenCalledTimes(1)
      expect(workerClient.listWorkers).not.toHaveBeenCalled()
    } finally {
      fixture.cleanup()
    }
  })
})
