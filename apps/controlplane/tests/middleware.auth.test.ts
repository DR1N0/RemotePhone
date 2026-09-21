import { Hono } from "hono"
import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createBearerAuthMiddleware, getAuthenticatedPrincipal, isLoopbackAddress } from "../src/middleware/auth.js"

describe("isLoopbackAddress", () => {
  it("recognizes IPv4 and IPv6 loopback addresses", () => {
    expect(isLoopbackAddress("127.0.0.1")).toBe(true)
    expect(isLoopbackAddress("::1")).toBe(true)
    expect(isLoopbackAddress("::ffff:127.0.0.1")).toBe(true)
  })

  it("rejects non-loopback or missing addresses", () => {
    expect(isLoopbackAddress("203.0.113.5")).toBe(false)
    expect(isLoopbackAddress(undefined)).toBe(false)
  })
})

describe("createBearerAuthMiddleware", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("attaches the authenticated principal and calls through on a valid token", async () => {
    const db = createTestDb()
    const authService = createAuthService({ db })
    const { token } = await authService.claimServer({ displayName: "Server Owner" })

    const app = new Hono()
    app.use("/protected", createBearerAuthMiddleware(authService))
    app.get("/protected", (c) => c.json(getAuthenticatedPrincipal(c)))

    const res = await app.request("/protected", {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.status).toBe(200)
    const body = await res.json() as { principalId: string }
    expect(body.principalId).toBeTruthy()
  })

  it("returns 401 for a missing or invalid Authorization header", async () => {
    const db = createTestDb()
    const authService = createAuthService({ db })
    await authService.claimServer({ displayName: "Server Owner" })

    const app = new Hono()
    app.use("/protected", createBearerAuthMiddleware(authService))
    app.get("/protected", (c) => c.json(getAuthenticatedPrincipal(c)))

    const missing = await app.request("/protected")
    expect(missing.status).toBe(401)

    const invalid = await app.request("/protected", { headers: { Authorization: "Bearer garbage" } })
    expect(invalid.status).toBe(401)
  })
})
