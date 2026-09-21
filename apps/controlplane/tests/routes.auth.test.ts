import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"

const workerClient = { listWorkers: async () => [], listTemplates: async () => [] }

function buildApp(overrides: { getClientAddress?: () => string | undefined } = {}) {
  const db = createTestDb()
  const authService = createAuthService({ db })
  const app = createServer({
    workerClient,
    authService,
    getClientAddress: overrides.getClientAddress ?? (() => "127.0.0.1"),
  })
  return { app, authService, db }
}

async function claim(app: ReturnType<typeof buildApp>["app"], displayName = "Server Owner") {
  const res = await app.request("/api/v1/auth/claim", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ displayName }),
  })
  return await res.json() as { principal: { id: string; isServerOwner: boolean }; token: string }
}

describe("auth routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  describe("POST /api/v1/auth/claim", () => {
    it("claims the server from a loopback address", async () => {
      const { app } = buildApp()
      const body = await claim(app)
      expect(body.principal.isServerOwner).toBe(true)
      expect(body.token.length).toBeGreaterThan(0)
    })

    it("rejects a claim from a non-loopback address", async () => {
      const { app } = buildApp({ getClientAddress: () => "203.0.113.5" })
      const res = await app.request("/api/v1/auth/claim", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ displayName: "Server Owner" }),
      })
      expect(res.status).toBe(403)
    })

    it("rejects a second claim once the server is already claimed", async () => {
      const { app } = buildApp()
      await claim(app)
      const res = await app.request("/api/v1/auth/claim", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ displayName: "Someone Else" }),
      })
      expect(res.status).toBe(409)
    })
  })

  describe("GET /api/v1/auth/me", () => {
    it("returns the authenticated principal", async () => {
      const { app } = buildApp()
      const { token } = await claim(app)

      const res = await app.request("/api/v1/auth/me", {
        headers: { Authorization: `Bearer ${token}` },
      })
      expect(res.status).toBe(200)
      const body = await res.json() as { principalId: string }
      expect(body.principalId).toBeTruthy()
    })

    it("returns 401 without a valid token", async () => {
      const { app } = buildApp()
      const res = await app.request("/api/v1/auth/me")
      expect(res.status).toBe(401)
    })
  })

  describe("POST /api/v1/auth/pairing-invites", () => {
    it("issues a pairing invite for the server owner", async () => {
      const { app } = buildApp()
      const { token } = await claim(app)

      const res = await app.request("/api/v1/auth/pairing-invites", {
        method: "POST",
        headers: { Authorization: `Bearer ${token}`, "content-type": "application/json" },
        body: JSON.stringify({ ttlMinutes: 30 }),
      })
      expect(res.status).toBe(201)
      const body = await res.json() as { token: string; expiresAt: string }
      expect(body.token.length).toBeGreaterThan(0)
      expect(body.expiresAt).toBeTruthy()
    })

    it("rejects a pairing invite request from a non-owner principal", async () => {
      const { app } = buildApp()
      const { token: ownerToken } = await claim(app)

      const inviteRes = await app.request("/api/v1/auth/pairing-invites", {
        method: "POST",
        headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
        body: JSON.stringify({ ttlMinutes: 30 }),
      })
      const { token: inviteToken } = await inviteRes.json() as { token: string }
      const redeemRes = await app.request("/api/v1/auth/pairing-invites/redeem", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token: inviteToken, displayName: "Guest", deviceLabel: "phone" }),
      })
      const { token: guestToken } = await redeemRes.json() as { token: string }

      const res = await app.request("/api/v1/auth/pairing-invites", {
        method: "POST",
        headers: { Authorization: `Bearer ${guestToken}`, "content-type": "application/json" },
        body: JSON.stringify({ ttlMinutes: 30 }),
      })
      expect(res.status).toBe(403)
    })
  })

  describe("POST /api/v1/auth/pairing-invites/redeem", () => {
    it("redeems a valid invite into a new principal and credential", async () => {
      const { app } = buildApp()
      const { token: ownerToken } = await claim(app)

      const inviteRes = await app.request("/api/v1/auth/pairing-invites", {
        method: "POST",
        headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
        body: JSON.stringify({ ttlMinutes: 30 }),
      })
      const { token: inviteToken } = await inviteRes.json() as { token: string }

      const res = await app.request("/api/v1/auth/pairing-invites/redeem", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token: inviteToken, displayName: "New User", deviceLabel: "phone" }),
      })
      expect(res.status).toBe(201)
      const body = await res.json() as { principal: { isServerOwner: boolean }; token: string }
      expect(body.principal.isServerOwner).toBe(false)
      expect(body.token.length).toBeGreaterThan(0)
    })

    it("rejects an unknown invite token", async () => {
      const { app } = buildApp()
      const res = await app.request("/api/v1/auth/pairing-invites/redeem", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token: "unknown", displayName: "X", deviceLabel: "d" }),
      })
      expect(res.status).toBe(400)
    })
  })

  describe("POST /api/v1/auth/credentials/:id/revoke", () => {
    it("revokes the caller's own credential", async () => {
      const { app } = buildApp()
      const { token } = await claim(app)

      const meRes = await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${token}` } })
      const { credentialId } = await meRes.json() as { credentialId: string }

      const revokeRes = await app.request(`/api/v1/auth/credentials/${credentialId}/revoke`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      })
      expect(revokeRes.status).toBe(204)

      const afterRes = await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${token}` } })
      expect(afterRes.status).toBe(401)
    })

    it("returns 404 for an unknown credential id", async () => {
      const { app } = buildApp()
      const { token } = await claim(app)

      const res = await app.request("/api/v1/auth/credentials/does-not-exist/revoke", {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      })
      expect(res.status).toBe(404)
    })
  })

  describe("POST /api/v1/principals/:id/ban and /unban", () => {
    it("lets the server owner ban and unban another principal", async () => {
      const { app } = buildApp()
      const { token: ownerToken } = await claim(app)

      const inviteRes = await app.request("/api/v1/auth/pairing-invites", {
        method: "POST",
        headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
        body: JSON.stringify({ ttlMinutes: 30 }),
      })
      const { token: inviteToken } = await inviteRes.json() as { token: string }
      const redeemRes = await app.request("/api/v1/auth/pairing-invites/redeem", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token: inviteToken, displayName: "Guest", deviceLabel: "phone" }),
      })
      const { principal: guest, token: guestToken } = await redeemRes.json() as {
        principal: { id: string }
        token: string
      }

      const banRes = await app.request(`/api/v1/principals/${guest.id}/ban`, {
        method: "POST",
        headers: { Authorization: `Bearer ${ownerToken}` },
      })
      expect(banRes.status).toBe(204)

      const meAfterBan = await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${guestToken}` } })
      expect(meAfterBan.status).toBe(401)

      const unbanRes = await app.request(`/api/v1/principals/${guest.id}/unban`, {
        method: "POST",
        headers: { Authorization: `Bearer ${ownerToken}` },
      })
      expect(unbanRes.status).toBe(204)

      const meAfterUnban = await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${guestToken}` } })
      expect(meAfterUnban.status).toBe(200)
    })

    it("rejects ban/unban from a non-owner caller", async () => {
      const { app } = buildApp()
      const { token: ownerToken } = await claim(app)

      const inviteRes = await app.request("/api/v1/auth/pairing-invites", {
        method: "POST",
        headers: { Authorization: `Bearer ${ownerToken}`, "content-type": "application/json" },
        body: JSON.stringify({ ttlMinutes: 30 }),
      })
      const { token: inviteToken } = await inviteRes.json() as { token: string }
      const redeemRes = await app.request("/api/v1/auth/pairing-invites/redeem", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ token: inviteToken, displayName: "Guest", deviceLabel: "phone" }),
      })
      const { principal: guest, token: guestToken } = await redeemRes.json() as {
        principal: { id: string }
        token: string
      }

      const res = await app.request(`/api/v1/principals/${guest.id}/ban`, {
        method: "POST",
        headers: { Authorization: `Bearer ${guestToken}` },
      })
      expect(res.status).toBe(403)
    })
  })
})
