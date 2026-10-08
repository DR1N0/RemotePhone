import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createUser } from "../src/persistence/repositories/users.js"
import { createClientApiServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
import { createAdminAuthFixture, setupAdminInService } from "./helpers/admin-auth.js"

const temporaryFixtures: Array<ReturnType<typeof createAdminAuthFixture>> = []

function createWorkerClient() {
  return {
    listWorkers: vi.fn().mockResolvedValue([{
      id: "local",
      kind: "local",
      hostOs: "macos",
      arch: "arm64",
      health: "ok",
      installedRuntimes: [{ runtimeClass: "android-studio-emulator", displayName: "Android Studio Emulator" }],
    }]),
    listTemplates: vi.fn().mockResolvedValue([{
      templateId: "macos-android-studio:Medium_Phone",
      runtimeClass: "android-studio-emulator",
      displayName: "Medium Phone",
      metadata: {},
    }]),
    startInstance: vi.fn().mockResolvedValue({ state: "starting", handle: null }),
    inspectInstance: vi.fn(),
    stopInstance: vi.fn(),
    createTransportSession: vi.fn(),
    updateTransportRole: vi.fn(),
    destroyTransportSession: vi.fn(),
    forwardOffer: vi.fn(),
    forwardICECandidate: vi.fn(),
  }
}

async function createAppWithSession() {
  const db = createTestDb()
  const fixture = createAdminAuthFixture()
  temporaryFixtures.push(fixture)
  const workerClient = createWorkerClient()
  const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
  const instanceService = createInstanceService({ db, workerClient })
  const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
  const app = createClientApiServer({ workerClient, instanceService, sessionService, authService, db, getClientAddress: () => "127.0.0.1" })
  const owner = await setupAdminInService(authService, fixture)
  const auth = { Authorization: `Bearer ${owner.token}`, "content-type": "application/json" }
  const createInstance = await app.request("/api/v1/instances", {
    method: "POST",
    headers: auth,
    body: JSON.stringify({
      name: "phone",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      },
      launchConfig: { headless: true, readOnly: true, wipeData: false },
    }),
  })
  const instanceId = (await createInstance.json() as { instance: { id: string } }).instance.id
  const createSession = await app.request("/api/v1/sessions", {
    method: "POST",
    headers: auth,
    body: JSON.stringify({ instanceId, ownerClientId: "owner-client" }),
  })
  expect(createSession.status).toBe(201)
  const created = await createSession.json() as {
    session: { id: string; attachments: Array<{ id: string; reconnectToken?: unknown }> }
    attachmentToken: string
  }
  const ownerAttachment = created.session.attachments[0]
  if (ownerAttachment == null) throw new Error("expected owner attachment")
  return { app, authService, created, db, instanceService, owner, ownerAttachment, ownerAuth: auth, sessionService }
}

async function redeemSessionAttachment(input: {
  app: ReturnType<typeof createClientApiServer>
  sessionId: string
  ownerAuth: HeadersInit
  mode: "guest" | "registered"
  headers: HeadersInit
}): Promise<{ attachment: { id: string; status: string }; attachmentToken: string }> {
  const invite = await input.app.request(`/api/v1/sessions/${input.sessionId}/invites`, {
    method: "POST",
    headers: input.ownerAuth,
  })
  expect(invite.status).toBe(201)
  const { token } = await invite.json() as { token: string }
  const redeemed = await input.app.request("/api/v1/session-invites/redeem", {
    method: "POST",
    headers: input.headers,
    body: JSON.stringify({ token, mode: input.mode, displayName: "Visitor" }),
  })
  expect(redeemed.status).toBe(201)
  return await redeemed.json() as { attachment: { id: string; status: string }; attachmentToken: string }
}

afterEach(() => {
  for (const fixture of temporaryFixtures.splice(0)) fixture.cleanup()
})

describe("session routes", () => {
  beforeEach(() => resetTestDb())

  it("creates an owner attachment credential without exposing reconnect tokens", async () => {
    const { created } = await createAppWithSession()

    expect(created.attachmentToken).toEqual(expect.any(String))
    expect(created.session).not.toHaveProperty("reconnectToken")
    expect(created.session.attachments[0]).not.toHaveProperty("reconnectToken")
  })

  it("requires the owning account or a matching attachment credential to read a session", async () => {
    const { app, authService, created, db, instanceService, ownerAttachment, ownerAuth, sessionService } = await createAppWithSession()
    const anonymous = await app.request(`/api/v1/sessions/${created.session.id}`)
    expect(anonymous.status).toBe(401)

    createUser(db, {
      id: "non-owner-admin",
      displayName: "Non-owner admin",
      userType: "admin",
      username: "nonowneradmin",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const nonOwnerToken = authService.issueAccountCredential("non-owner-admin", "test")
    const nonOwner = await app.request(`/api/v1/sessions/${created.session.id}`, {
      headers: { Authorization: `Bearer ${nonOwnerToken}` },
    })
    expect(nonOwner.status).toBe(404)

    createUser(db, {
      id: "unrelated-registered",
      displayName: "Unrelated registered user",
      userType: "registered",
      username: "unrelatedregistered",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const registeredToken = authService.issueAccountCredential("unrelated-registered", "test")
    const unrelatedRegistered = await app.request(`/api/v1/sessions/${created.session.id}`, {
      headers: { Authorization: `Bearer ${registeredToken}` },
    })
    expect(unrelatedRegistered.status).toBe(404)

    createUser(db, {
      id: "other-owner",
      displayName: "Other owner",
      userType: "registered",
      username: "otherowner",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const secondInstance = await instanceService.createManagedInstance({
      name: "second-phone",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      },
      launchConfig: { headless: true, readOnly: true, wipeData: false },
    }, "other-owner")
    const otherToken = authService.issueAccountCredential("other-owner", "test")
    const otherCaller = await authService.authenticate(otherToken)
    if (otherCaller == null) throw new Error("expected other owner caller")
    const secondSession = await sessionService.createSession({
      instanceId: secondInstance.id,
      ownerClientId: "second-owner-client",
      caller: otherCaller,
    })
    const crossSessionRead = await app.request(`/api/v1/sessions/${created.session.id}`, {
      headers: { Authorization: `Bearer ${secondSession.attachmentToken}` },
    })
    expect(crossSessionRead.status).toBe(404)
    const absentRead = await app.request("/api/v1/sessions/not-a-real-session", {
      headers: { Authorization: `Bearer ${nonOwnerToken}` },
    })
    expect(absentRead.status).toBe(404)
    const crossSessionHeaders = { Authorization: `Bearer ${secondSession.attachmentToken}` }
    const crossSessionDisconnect = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/disconnect`,
      { method: "POST", headers: crossSessionHeaders },
    )
    const crossSessionResume = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/resume`,
      { method: "POST", headers: crossSessionHeaders },
    )
    const crossSessionRefresh = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/credentials/refresh`,
      { method: "POST", headers: crossSessionHeaders },
    )
    expect(crossSessionDisconnect.status).toBe(403)
    expect(crossSessionResume.status).toBe(403)
    expect(crossSessionRefresh.status).toBe(403)

    const ownerRead = await app.request(`/api/v1/sessions/${created.session.id}`, { headers: ownerAuth })
    expect(ownerRead.status).toBe(200)
    const attachedRead = await app.request(`/api/v1/sessions/${created.session.id}`, {
      headers: { Authorization: `Bearer ${created.attachmentToken}` },
    })
    expect(attachedRead.status).toBe(200)
    expect((await attachedRead.json() as { session: object }).session).not.toHaveProperty("reconnectToken")
    expect(secondInstance.id).toBeTruthy()
  })

  it("retires ID-only join and resume paths and does not authorize mutation from IDs", async () => {
    const { app, created, ownerAttachment } = await createAppWithSession()
    const join = await app.request(`/api/v1/sessions/${created.session.id}/join`, { method: "POST" })
    const oldResume = await app.request(`/api/v1/sessions/${created.session.id}/resume`, { method: "POST" })
    const disconnect = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/disconnect`,
      { method: "POST" },
    )
    const promote = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/promote`,
      { method: "POST" },
    )

    expect(join.status).toBe(410)
    expect(oldResume.status).toBe(410)
    expect(disconnect.status).toBe(401)
    expect(promote.status).toBe(401)
  })

  it("resumes a disconnected attachment and rotates its credential", async () => {
    const { app, created, ownerAttachment } = await createAppWithSession()
    const disconnect = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/disconnect`,
      { method: "POST", headers: { Authorization: `Bearer ${created.attachmentToken}` } },
    )
    expect(disconnect.status).toBe(200)

    const resumed = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/resume`,
      { method: "POST", headers: { Authorization: `Bearer ${created.attachmentToken}` } },
    )
    expect(resumed.status).toBe(200)
    const resumeBody = await resumed.json() as { attachment: { status: string }; attachmentToken: string }
    expect(resumeBody.attachment.status).toBe("connected")
    expect(resumeBody.attachmentToken).not.toBe(created.attachmentToken)

    const oldToken = await app.request(`/api/v1/sessions/${created.session.id}`, {
      headers: { Authorization: `Bearer ${created.attachmentToken}` },
    })
    expect(oldToken.status).toBe(401)

    const refreshed = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/credentials/refresh`,
      { method: "POST", headers: { Authorization: `Bearer ${resumeBody.attachmentToken}` } },
    )
    expect(refreshed.status).toBe(200)
    const refreshBody = await refreshed.json() as { attachmentToken: string }
    expect(refreshBody.attachmentToken).not.toBe(resumeBody.attachmentToken)
  })

  it("lets registered and guest invite attachments resume and refresh themselves", async () => {
    const { app, authService, created, db, ownerAuth } = await createAppWithSession()
    createUser(db, {
      id: "registered-observer",
      displayName: "Registered observer",
      userType: "registered",
      username: "registeredobserver",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const registered = await redeemSessionAttachment({
      app,
      sessionId: created.session.id,
      ownerAuth,
      mode: "registered",
      headers: { Authorization: `Bearer ${authService.issueAccountCredential("registered-observer", "test")}`, "content-type": "application/json" },
    })
    const guest = await redeemSessionAttachment({
      app,
      sessionId: created.session.id,
      ownerAuth,
      mode: "guest",
      headers: { "content-type": "application/json" },
    })

    for (const joined of [registered, guest]) {
      const disconnect = await app.request(
        `/api/v1/sessions/${created.session.id}/attachments/${joined.attachment.id}/disconnect`,
        { method: "POST", headers: { Authorization: `Bearer ${joined.attachmentToken}` } },
      )
      expect(disconnect.status).toBe(200)
      const resumed = await app.request(
        `/api/v1/sessions/${created.session.id}/attachments/${joined.attachment.id}/resume`,
        { method: "POST", headers: { Authorization: `Bearer ${joined.attachmentToken}` } },
      )
      expect(resumed.status).toBe(200)
      const { attachmentToken } = await resumed.json() as { attachmentToken: string }
      const refreshed = await app.request(
        `/api/v1/sessions/${created.session.id}/attachments/${joined.attachment.id}/credentials/refresh`,
        { method: "POST", headers: { Authorization: `Bearer ${attachmentToken}` } },
      )
      expect(refreshed.status).toBe(200)
    }
  })

  it("allows the owner account to force-disconnect another attachment", async () => {
    const { app, created, ownerAuth } = await createAppWithSession()
    const invited = await redeemSessionAttachment({
      app,
      sessionId: created.session.id,
      ownerAuth,
      mode: "guest",
      headers: { "content-type": "application/json" },
    })

    const response = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${invited.attachment.id}/disconnect`,
      { method: "POST", headers: ownerAuth },
    )

    expect(response.status).toBe(200)
    expect((await response.json() as { session: { attachments: Array<{ id: string; status: string }> } }).session.attachments)
      .toContainEqual(expect.objectContaining({ id: invited.attachment.id, status: "disconnected" }))
  })

  it("restricts moderation to the owner account and keeps the owner attachment immutable", async () => {
    const { app, authService, created, db, ownerAttachment, ownerAuth } = await createAppWithSession()
    const invited = await redeemSessionAttachment({
      app,
      sessionId: created.session.id,
      ownerAuth,
      mode: "guest",
      headers: { "content-type": "application/json" },
    })
    createUser(db, {
      id: "foreign-admin",
      displayName: "Foreign admin",
      userType: "admin",
      username: "foreignadmin",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const foreignHeaders = { Authorization: `Bearer ${authService.issueAccountCredential("foreign-admin", "test")}` }
    for (const action of ["promote", "demote"] as const) {
      const foreign = await app.request(
        `/api/v1/sessions/${created.session.id}/attachments/${invited.attachment.id}/${action}`,
        { method: "POST", headers: foreignHeaders },
      )
      expect(foreign.status).toBe(403)
      const immutable = await app.request(
        `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/${action}`,
        { method: "POST", headers: ownerAuth },
      )
      expect(immutable.status).toBe(400)
    }

    const promoted = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${invited.attachment.id}/promote`,
      { method: "POST", headers: ownerAuth },
    )
    expect(promoted.status).toBe(200)
    const demoted = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${invited.attachment.id}/demote`,
      { method: "POST", headers: ownerAuth },
    )
    expect(demoted.status).toBe(200)
  })

  it("does not allow an owner account to resume or refresh and rejects a closed session", async () => {
    const { app, created, ownerAttachment, ownerAuth, sessionService } = await createAppWithSession()
    const accountResume = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/resume`,
      { method: "POST", headers: ownerAuth },
    )
    expect(accountResume.status).toBe(403)

    await sessionService.closeSession(created.session.id)
    const closedRefresh = await app.request(
      `/api/v1/sessions/${created.session.id}/attachments/${ownerAttachment.id}/credentials/refresh`,
      { method: "POST", headers: { Authorization: `Bearer ${created.attachmentToken}` } },
    )
    expect(closedRefresh.status).toBe(401)
  })
})
