import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { getCredentialByTokenHash } from "../src/persistence/repositories/credentials.js"
import { createUser, getUserById, listUsers } from "../src/persistence/repositories/users.js"
import { createServer } from "../src/server.js"
import { createAuthService } from "../src/services/auth-service.js"
import { createInstanceService } from "../src/services/instance-service.js"
import { createSessionService } from "../src/services/session-service.js"
import { hashToken } from "../src/services/token.js"
import { createAdminAuthFixture, setupAdmin } from "./helpers/admin-auth.js"

const temporaryFixtures: Array<ReturnType<typeof createAdminAuthFixture>> = []

function createWorkerClient() {
  return {
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
    listTemplates: vi.fn().mockResolvedValue([
      {
        templateId: "macos-android-studio:Medium_Phone",
        runtimeClass: "android-studio-emulator",
        displayName: "Medium Phone",
        metadata: { avdName: "Medium_Phone" },
      },
    ]),
    startInstance: vi.fn().mockImplementation(async (input: { instanceId: string; templateId: string; runtimeClass: string }) => ({
      state: "starting",
      handle: {
        instanceId: input.instanceId,
        templateId: input.templateId,
        runtimeClass: input.runtimeClass,
        workerId: "local",
        pid: 4321,
        consolePort: 5600,
        adbPort: 5601,
        grpcPort: 8554,
      },
    })),
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
  const workerClient = createWorkerClient()
  const instanceService = createInstanceService({ db, workerClient })
    const fixture = createAdminAuthFixture()
  temporaryFixtures.push(fixture)
  const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })

  const sessionService = createSessionService({ db, authService, defaultOwnerAbsenceTimeoutMs: 300_000 })
  const app = createServer({
    workerClient,
    instanceService,
    sessionService,
    authService,
    db,
    baseUrl: "http://localhost:3000",
    getClientAddress: () => "127.0.0.1",
  })
  const owner = await setupAdmin(app, fixture)
  const auth = { Authorization: `Bearer ${owner.token}`, "content-type": "application/json" }

  const createInstanceRes = await app.request("/api/v1/instances", {
    method: "POST",
    headers: auth,
    body: JSON.stringify({
      name: "phone-1",
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
  const instanceId = (await createInstanceRes.json() as { instance: { id: string } }).instance.id
  await app.request(`/api/v1/instances/${instanceId}/start`, { method: "POST", headers: auth })
  const createSessionRes = await app.request("/api/v1/sessions", {
    method: "POST",
    headers: auth,
    body: JSON.stringify({ instanceId, ownerClientId: "owner-1" }),
  })
  const sessionId = (await createSessionRes.json() as { session: { id: string } }).session.id
  return { app, authService, db, owner, ownerAuth: auth, sessionId }
}

async function issueInvite(app: ReturnType<typeof createServer>, sessionId: string, headers: HeadersInit) {
  const response = await app.request(`/api/v1/sessions/${sessionId}/invites`, { method: "POST", headers })
  expect(response.status).toBe(201)
  return await response.json() as { token: string; expiresAt: string }
}

afterEach(() => {
  for (const fixture of temporaryFixtures.splice(0)) {
    fixture.cleanup()
  }
})

describe("session invite routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("requires the session owner's account credential to issue an invite", async () => {
    const { app, authService, db, ownerAuth, sessionId } = await createAppWithSession()
    const unauthenticated = await app.request(`/api/v1/sessions/${sessionId}/invites`, { method: "POST" })
    expect(unauthenticated.status).toBe(401)

    createUser(db, {
      id: "non-owner",
      displayName: "Non Owner",
      userType: "registered",
      username: "nonowner",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const nonOwner = authService.issueAccountCredential("non-owner", "test")
    const forbidden = await app.request(`/api/v1/sessions/${sessionId}/invites`, {
      method: "POST",
      headers: { Authorization: `Bearer ${nonOwner}` },
    })
    expect(forbidden.status).toBe(403)

    const invite = await issueInvite(app, sessionId, ownerAuth)
    expect(invite.token).toHaveLength(43)
    const row = db.$client.prepare("SELECT token_hash FROM session_invites").get() as { token_hash: string }
    expect(row.token_hash).toBe(hashToken(invite.token))
  })

  it("uses only the authenticated account for registered redemption", async () => {
    const { app, authService, db, owner, ownerAuth, sessionId } = await createAppWithSession()
    createUser(db, {
      id: "registered-user",
      displayName: "Registered",
      userType: "registered",
      username: "registered",
      passwordHash: "hash",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const registeredToken = authService.issueAccountCredential("registered-user", "test")
    const invite = await issueInvite(app, sessionId, ownerAuth)
    const missingCredential = await app.request("/api/v1/session-invites/redeem", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ token: invite.token, mode: "registered" }),
    })
    expect(missingCredential.status).toBe(401)

    const response = await app.request("/api/v1/session-invites/redeem", {
      method: "POST",
      headers: { Authorization: `Bearer ${registeredToken}`, "content-type": "application/json" },
      body: JSON.stringify({ token: invite.token, mode: "registered", requesterUserId: owner.user.id }),
    })

    expect(response.status).toBe(201)
    const joined = await response.json() as { user: { id: string; username: string }; attachmentToken: string }
    expect(joined.user).toEqual(expect.objectContaining({ id: "registered-user", username: "registered" }))
    expect(await authService.authenticate(joined.attachmentToken)).toMatchObject({ userId: "registered-user", scope: "attachment", sessionId })
  })

  it("rejects every Authorization header for guest redemption and creates an independent guest without one", async () => {
    const { app, db, ownerAuth, sessionId } = await createAppWithSession()
    const invite = await issueInvite(app, sessionId, ownerAuth)
    const withAccount = await app.request("/api/v1/session-invites/redeem", {
      method: "POST",
      headers: ownerAuth,
      body: JSON.stringify({ token: invite.token, mode: "guest", displayName: "Visitor" }),
    })
    expect(withAccount.status).toBe(400)
    expect(listUsers(db)).toHaveLength(1)

    const guest = await app.request("/api/v1/session-invites/redeem", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ token: invite.token, mode: "guest", displayName: "Visitor" }),
    })
    expect(guest.status).toBe(201)
    const joined = await guest.json() as { user: { id: string; type: string; username: string | null }; attachment: { role: string }; attachmentToken: string }
    expect(joined.user).toMatchObject({ type: "guest", username: null })
    expect(getUserById(db, joined.user.id)).toMatchObject({ passwordHash: null, username: null })
    expect(joined.attachment).toMatchObject({ role: "observer" })
    expect(joined.attachmentToken).toBeTruthy()
  })

  it("rejects attachment credentials for registered redemption and has no legacy token redirect", async () => {
    const { app, authService, ownerAuth, sessionId } = await createAppWithSession()
    const guestInvite = await issueInvite(app, sessionId, ownerAuth)
    const guestResponse = await app.request("/api/v1/session-invites/redeem", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ token: guestInvite.token, mode: "guest" }),
    })
    const guestJoined = await guestResponse.json() as { attachmentToken: string }
    const registeredInvite = await issueInvite(app, sessionId, ownerAuth)

    const response = await app.request("/api/v1/session-invites/redeem", {
      method: "POST",
      headers: { Authorization: `Bearer ${guestJoined.attachmentToken}`, "content-type": "application/json" },
      body: JSON.stringify({ token: registeredInvite.token, mode: "registered" }),
    })
    expect(response.status).toBe(403)
    expect(await authService.authenticate(guestJoined.attachmentToken)).toMatchObject({ scope: "attachment" })

    const legacy = await app.request(`/session/${sessionId}?token=${registeredInvite.token}`, { redirect: "manual" })
    expect(legacy.status).toBe(404)
    expect(legacy.headers.get("location")).toBeNull()
  })
})
