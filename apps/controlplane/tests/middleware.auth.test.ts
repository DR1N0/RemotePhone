import { Hono } from "hono"
import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment } from "../src/persistence/repositories/attachments.js"
import { createCredential } from "../src/persistence/repositories/credentials.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createSession } from "../src/persistence/repositories/sessions.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createAuthService } from "../src/services/auth-service.js"
import { hashToken } from "../src/services/token.js"
import {
  createBearerAuthMiddleware,
  getAuthenticatedUser,
  getAuthenticatedUser,
  isLoopbackAddress,
  requireAccountAuth,
  requireAttachmentAuth,
} from "../src/middleware/auth.js"

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

function createScopedApp(authService: ReturnType<typeof createAuthService>): Hono {
  const app = new Hono()
  app.use("*", createBearerAuthMiddleware(authService))
  app.get("/account", requireAccountAuth, (c) => c.json(getAuthenticatedUser(c)))
  app.get("/attachment/:sessionId/:attachmentId", requireAttachmentAuth, (c) => {
    const caller = getAuthenticatedUser(c)
    if (caller.sessionId !== c.req.param("sessionId") || caller.attachmentId !== c.req.param("attachmentId")) {
      return c.json({ error: "attachment credential does not match target" }, 403)
    }
    return c.json(caller)
  })
  return app
}

async function seedScopedCredentials(db: ReturnType<typeof createTestDb>) {
  const authService = createAuthService({
    db,
    now: () => new Date("2026-10-02T12:00:00.000Z"),
  })
  const user = {
    id: "server-owner",
  }
  createUser(db, {
    id: user.id,
    displayName: "Server Owner",
    userType: "admin",
    username: "admin",
    enrollmentRequired: false,
    isServerOwner: true,
    status: "active",
  })
  const accountToken = authService.issueAccountCredential(user.id, "test-device")

  createInstance(db, {
    id: "inst_auth",
    ownerId: user.id,
    name: "auth-instance",
    runtimeClass: "android-studio-emulator",
    templateId: "macos-android-studio:Medium_Phone",
    placementPolicy: { kind: "local-only" },
    resourceProfile: {
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    },
    launchConfig: { headless: true, readOnly: true, wipeData: false },
    desiredState: "stopped",
    observedState: "stopped",
    runtimeHandle: null,
    stateDataRef: null,
    lastError: null,
  })
  createSession(db, {
    id: "sess_auth",
    instanceId: "inst_auth",
    status: "active",
    ownerAbsenceDeadline: null,
    ownerAbsenceTimeoutMs: null,
    reconnectToken: "session-reconnect-token",
  })
  createAttachment(db, {
    id: "att_auth",
    sessionId: "sess_auth",
    userId: user.id,
    clientId: "attached-client",
    role: "observer",
    status: "connected",
    reconnectToken: "attachment-reconnect-token",
  })

  const attachmentToken = "attachment-token"
  createCredential(db, {
    id: "attachment-credential",
    userId: user.id,
    tokenHash: hashToken(attachmentToken),
    deviceLabel: "attached-client",
    scope: "attachment",
    sessionId: "sess_auth",
    attachmentId: "att_auth",
    notBefore: "2026-10-02T11:00:00.000Z",
    expiresAt: "2026-10-03T12:00:00.000Z",
    revokedAt: null,
  })

  return { accountToken, attachmentToken, authService, user }
}

function seedAdmin(db: ReturnType<typeof createTestDb>, authService: ReturnType<typeof createAuthService>): string {
  createUser(db, {
    id: "server-owner",
    displayName: "Server Owner",
    userType: "admin",
    username: "admin",
    enrollmentRequired: false,
    isServerOwner: true,
    status: "active",
  })
  return authService.issueAccountCredential("server-owner", "test-device")
}

describe("createBearerAuthMiddleware", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("attaches the authenticated user and calls through on a valid token", async () => {
    const db = createTestDb()
    const authService = createAuthService({ db })
    const token = seedAdmin(db, authService)

    const app = new Hono()
    app.use("/protected", createBearerAuthMiddleware(authService))
    app.get("/protected", (c) => c.json(getAuthenticatedUser(c)))

    const res = await app.request("/protected", {
      headers: { Authorization: `Bearer ${token}` },
    })
    expect(res.status).toBe(200)
    const body = await res.json() as { userId: string }
    expect(body.userId).toBeTruthy()
  })

  it("returns 401 for a missing or invalid Authorization header", async () => {
    const db = createTestDb()
    const authService = createAuthService({ db })
    seedAdmin(db, authService)

    const app = new Hono()
    app.use("/protected", createBearerAuthMiddleware(authService))
    app.get("/protected", (c) => c.json(getAuthenticatedUser(c)))

    const missing = await app.request("/protected")
    expect(missing.status).toBe(401)

    const invalid = await app.request("/protected", { headers: { Authorization: "Bearer garbage" } })
    expect(invalid.status).toBe(401)
  })

  it("allows each credential only on its authorized scope and bound attachment", async () => {
    const db = createTestDb()
    const { accountToken, attachmentToken, authService } = await seedScopedCredentials(db)
    const app = createScopedApp(authService)

    expect(await app.request("/account", {
      headers: { Authorization: `Bearer ${accountToken}` },
    })).toHaveProperty("status", 200)
    expect(await app.request("/attachment/sess_auth/att_auth", {
      headers: { Authorization: `Bearer ${accountToken}` },
    })).toHaveProperty("status", 403)
    expect(await app.request("/account", {
      headers: { Authorization: `Bearer ${attachmentToken}` },
    })).toHaveProperty("status", 403)
    expect(await app.request("/attachment/sess_auth/att_auth", {
      headers: { Authorization: `Bearer ${attachmentToken}` },
    })).toHaveProperty("status", 200)
    expect(await app.request("/attachment/other-session/att_auth", {
      headers: { Authorization: `Bearer ${attachmentToken}` },
    })).toHaveProperty("status", 403)
    expect(await app.request("/attachment/sess_auth/other-attachment", {
      headers: { Authorization: `Bearer ${attachmentToken}` },
    })).toHaveProperty("status", 403)
  })

  it("returns 401 for expired and not-yet-valid credentials", async () => {
    const db = createTestDb()
    const { authService, user } = await seedScopedCredentials(db)
    const expiredToken = "expired-token"
    const futureToken = "future-token"
    createCredential(db, {
      id: "expired-credential",
      userId: user.id,
      tokenHash: hashToken(expiredToken),
      deviceLabel: "expired-device",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: "2026-10-02T11:59:59.000Z",
      revokedAt: null,
    })
    createCredential(db, {
      id: "future-credential",
      userId: user.id,
      tokenHash: hashToken(futureToken),
      deviceLabel: "future-device",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: "2026-10-02T12:00:01.000Z",
      expiresAt: null,
      revokedAt: null,
    })
    const app = createScopedApp(authService)

    expect(await app.request("/account", {
      headers: { Authorization: `Bearer ${expiredToken}` },
    })).toHaveProperty("status", 401)
    expect(await app.request("/account", {
      headers: { Authorization: `Bearer ${futureToken}` },
    })).toHaveProperty("status", 401)
  })

  it("rejects callers that request authenticated user context before bearer middleware", async () => {
    const app = new Hono()
    app.get("/unprotected", (c) => {
      expect(() => getAuthenticatedUser(c)).toThrow(
        "getAuthenticatedUser called without createBearerAuthMiddleware in the request chain",
      )
      return c.body(null, 204)
    })

    expect((await app.request("/unprotected")).status).toBe(204)
  })
})
