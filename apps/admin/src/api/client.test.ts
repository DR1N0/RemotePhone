import { afterEach, describe, expect, it, vi } from "vitest"
import { ApiError, api } from "./client.js"
import { adminSession } from "../auth/session.js"

function mockResponse(status: number, body?: string) {
  return new Response(body, { status, headers: { "content-type": "application/json" } })
}

describe("admin API client", () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it("sends the session credential as a bearer token", async () => {
    adminSession.setToken("account-token")
    const fetch = vi.fn().mockResolvedValue(mockResponse(200, JSON.stringify({ instances: [] })))
    vi.stubGlobal("fetch", fetch)

    await api.listAdminInstances()

    expect(fetch).toHaveBeenCalledWith("/api/v1/admin/instances", expect.objectContaining({
      headers: expect.objectContaining({ Authorization: "Bearer account-token" }),
    }))
  })

  it("clears the web UI credential on 401 and exposes its status", async () => {
    adminSession.setToken("expired-token")
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(mockResponse(401, "expired credential")))

    const error = await api.listAdminInstances().catch((cause: unknown) => cause)

    expect(error).toBeInstanceOf(ApiError)
    expect(error).toMatchObject({ status: 401, message: "expired credential" })
    expect(adminSession.getToken()).toBeNull()
  })

  it("keeps a valid non-admin credential on management 403 and signals authorization loss", async () => {
    adminSession.setToken("registered-token")
    const onAuthorizationLoss = vi.fn()
    window.addEventListener("mako:admin-authorization-lost", onAuthorizationLoss)
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(mockResponse(403, "admin required")))

    await expect(api.listAdminInstances()).rejects.toMatchObject({ status: 403 })
    expect(adminSession.getToken()).toBe("registered-token")
    expect(onAuthorizationLoss).toHaveBeenCalledOnce()
    window.removeEventListener("mako:admin-authorization-lost", onAuthorizationLoss)
  })

  it("accepts 204 responses without attempting JSON parsing", async () => {
    const fetch = vi.fn().mockResolvedValue(mockResponse(204))
    vi.stubGlobal("fetch", fetch)

    await expect(api.logout()).resolves.toBeUndefined()
  })

  it("uses the typed admin endpoints and only sends pagination cursors when present", async () => {
    adminSession.setToken("admin-token")
    const fetch = vi.fn()
      .mockResolvedValueOnce(mockResponse(200, JSON.stringify({ items: [], nextCursor: null })))
      .mockResolvedValueOnce(mockResponse(201, JSON.stringify({ token: "one-use", expiresAt: "2026-10-03T00:00:00.000Z" })))
    vi.stubGlobal("fetch", fetch)

    await api.listUsers({ limit: 20, cursor: null })
    await api.createRegistrationInvite()

    expect(fetch).toHaveBeenNthCalledWith(1, "/api/v1/admin/users?limit=20", expect.objectContaining({
      headers: expect.objectContaining({ Authorization: "Bearer admin-token" }),
    }))
    expect(fetch).toHaveBeenNthCalledWith(2, "/api/v1/auth/registration-invites", expect.objectContaining({ method: "POST" }))
  })

  it("uses a supplied attachment bearer for diagnostics instead of the admin account token", async () => {
    adminSession.setToken("admin-token")
    const fetch = vi.fn().mockResolvedValue(mockResponse(200, JSON.stringify({ session: { id: "session-1" } })))
    vi.stubGlobal("fetch", fetch)

    await api.getDiagnosticSession("session-1", "attachment-token")

    expect(fetch).toHaveBeenCalledWith("/api/v1/sessions/session-1", expect.objectContaining({
      headers: expect.objectContaining({ Authorization: "Bearer attachment-token" }),
    }))
  })

  it("forwards diagnostic ICE with the supplied attachment bearer", async () => {
    adminSession.setToken("admin-token")
    const fetch = vi.fn().mockResolvedValue(mockResponse(204))
    vi.stubGlobal("fetch", fetch)

    await api.sendDiagnosticICECandidate("session-1", "attachment-1", "candidate:1", "attachment-token")

    expect(fetch).toHaveBeenCalledWith("/api/v1/signaling/ice-candidate", expect.objectContaining({
      method: "POST",
      body: JSON.stringify({ sessionId: "session-1", attachmentId: "attachment-1", candidate: "candidate:1" }),
      headers: expect.objectContaining({ Authorization: "Bearer attachment-token" }),
    }))
  })
})
