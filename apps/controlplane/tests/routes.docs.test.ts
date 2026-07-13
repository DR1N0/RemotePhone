import { describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"

function createWorkerClient() {
  return {
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
    startInstance: vi.fn(),
    inspectInstance: vi.fn(),
    stopInstance: vi.fn(),
    createTransportSession: vi.fn(),
  }
}

describe("docs routes", () => {
  it("serves an OpenAPI document describing the public API", async () => {
    resetTestDb()
    const workerClient = createWorkerClient()
    const db = createTestDb()
    const instanceService = createInstanceService({ db, workerClient })
    const sessionService = createSessionService({ db, idleTtlMs: 300_000 })
    const app = createServer({ workerClient, instanceService, sessionService })

    const res = await app.request("/openapi.json")

    expect(res.status).toBe(200)
    expect(await res.json()).toMatchObject({
      openapi: "3.1.0",
      info: {
        title: "Mako Server API",
        version: "0.1.0",
      },
      paths: expect.objectContaining({
        "/": expect.any(Object),
        "/healthz": expect.any(Object),
        "/api/v1/workers": expect.any(Object),
        "/api/v1/templates": expect.any(Object),
        "/api/v1/instances": expect.objectContaining({
          get: expect.any(Object),
          post: expect.any(Object),
        }),
        "/api/v1/instances/{id}": expect.objectContaining({
          get: expect.any(Object),
          delete: expect.any(Object),
        }),
        "/api/v1/instances/{id}/start": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/instances/{id}/stop": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/sessions": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/sessions/{id}": expect.objectContaining({
          get: expect.any(Object),
        }),
        "/api/v1/sessions/{id}/join": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/sessions/{id}/resume": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/sessions/{id}/attachments/{attachmentId}/disconnect": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/sessions/{id}/attachments/{attachmentId}/promote": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/sessions/{id}/attachments/{attachmentId}/demote": expect.objectContaining({
          post: expect.any(Object),
        }),
        "/api/v1/signaling/session": expect.any(Object),
      }),
    })
  })

  it("serves swagger ui", async () => {
    const app = createServer({
      workerClient: {
        listWorkers: vi.fn().mockResolvedValue([]),
        listTemplates: vi.fn().mockResolvedValue([]),
      },
    })

    const res = await app.request("/docs")

    expect(res.status).toBe(200)
    expect(res.headers.get("content-type")).toContain("text/html")
    expect(await res.text()).toContain("SwaggerUIBundle")
  })
})
