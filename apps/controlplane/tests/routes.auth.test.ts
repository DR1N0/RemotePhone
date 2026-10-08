import { readFileSync } from "node:fs"
import { afterEach, beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment } from "../src/persistence/repositories/attachments.js"
import { getCredentialByTokenHash } from "../src/persistence/repositories/credentials.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { createSession } from "../src/persistence/repositories/sessions.js"
import { countAdmins, createUser, getUserById, listUsers } from "../src/persistence/repositories/users.js"
import { createServer } from "../src/server.js"
import { hashPassword } from "../src/services/password.js"
import { createAdminService } from "../src/services/admin-service.js"
import { createAuthService } from "../src/services/auth-service.js"
import { hashToken } from "../src/services/token.js"
import { createAdminAuthFixture, setupAdmin } from "./helpers/admin-auth.js"

const workerClient = { listWorkers: async () => [], listTemplates: async () => [] }
const fixtures: Array<{ cleanup: () => void }> = []

function buildApp(overrides: { getClientAddress?: () => string | undefined } = {}) {
  const db = createTestDb()
  const fixture = createAdminAuthFixture()
  fixtures.push(fixture)
  const authService = createAuthService({ db, bootstrapSecretPath: fixture.bootstrapSecretPath })
  const adminService = createAdminService({ db })
  const app = createServer({
    workerClient,
    authService,
    adminService,
    getClientAddress: overrides.getClientAddress ?? (() => "127.0.0.1"),
  })
  return { app, authService, db, fixture }
}

async function login(app: ReturnType<typeof buildApp>["app"], input: Record<string, string> = {}) {
  return await app.request("/api/v1/auth/login", {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({
      username: "admin",
      password: "long-password-for-test",
      deviceLabel: "login-device",
      ...input,
    }),
  })
}

describe("auth routes", () => {
  beforeEach(() => {
    resetTestDb()
  })

  afterEach(() => {
    for (const fixture of fixtures.splice(0)) {
      fixture.cleanup()
    }
  })

  it("reports an unclaimed server without exposing the bootstrap secret", async () => {
    const { app } = buildApp()

    const response = await app.request("/api/v1/auth/status")

    expect(response.status).toBe(200)
    expect(await response.json()).toEqual({ claimed: false })
  })

  it("rejects a wrong bootstrap secret without creating a user", async () => {
    const { app, db, fixture } = buildApp()

    const response = await app.request("/api/v1/auth/setup", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        username: "admin",
        password: "long-password-for-test",
        deviceLabel: "local",
        bootstrapSecret: "wrong-secret",
      }),
    })

    expect(response.status).toBe(403)
    expect(listUsers(db)).toEqual([])
    expect(readFileSync(fixture.bootstrapSecretPath, "utf8")).toBeTruthy()
  })

  it("rejects a valid setup secret from remote or proxied paths, then permits a direct local setup", async () => {
    const remote = buildApp({ getClientAddress: () => "203.0.113.25" })
    const remoteSecret = readFileSync(remote.fixture.bootstrapSecretPath, "utf8")

    const remoteResponse = await remote.app.request("https://admin.example.test/api/v1/auth/setup", {
      method: "POST",
      headers: { "content-type": "application/json", host: "admin.example.test" },
      body: JSON.stringify({ username: "remote_admin", password: "long-password-for-test", deviceLabel: "remote", bootstrapSecret: remoteSecret }),
    })

    expect(remoteResponse.status).toBe(403)
    expect(listUsers(remote.db)).toEqual([])

    const proxied = buildApp()
    const proxiedSecret = readFileSync(proxied.fixture.bootstrapSecretPath, "utf8")
    const proxiedResponse = await proxied.app.request("https://admin.example.test/api/v1/auth/setup", {
      method: "POST",
      headers: { "content-type": "application/json", host: "admin.example.test" },
      body: JSON.stringify({ username: "proxy_admin", password: "long-password-for-test", deviceLabel: "proxy", bootstrapSecret: proxiedSecret }),
    })

    expect(proxiedResponse.status).toBe(403)
    expect(listUsers(proxied.db)).toEqual([])

    const local = buildApp()
    const localSetup = await setupAdmin(local.app, local.fixture, { username: "local_admin", deviceLabel: "local" })
    expect(localSetup.user).toMatchObject({ type: "admin", username: "local_admin" })
    expect(countAdmins(local.db)).toBe(1)
  })

  it("sets up the first admin and an account-scoped credential exactly once", async () => {
    const { app, db, fixture } = buildApp()

    const setup = await setupAdmin(app, fixture, { username: "root", deviceLabel: "local" })

    expect(setup.user).toMatchObject({ type: "admin", username: "root" })
    expect(getCredentialByTokenHash(db, hashToken(setup.token))).toMatchObject({
      userId: setup.user.id,
      scope: "account",
      sessionId: null,
      attachmentId: null,
    })
    expect(countAdmins(db)).toBe(1)
    const status = app.request("/api/v1/auth/status")
    expect(await status.json()).toEqual({ claimed: true })
    expect(() => readFileSync(fixture.bootstrapSecretPath, "utf8")).toThrow()
  })

  it("allows only one concurrent setup", async () => {
    const { app, db, fixture } = buildApp()
    const secret = readFileSync(fixture.bootstrapSecretPath, "utf8")
    const request = (username: string) => app.request("/api/v1/auth/setup", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ username, password: "long-password-for-test", deviceLabel: "local", bootstrapSecret: secret }),
    })

    const responses = await Promise.all([request("admin_one"), request("admin_two")])

    expect(responses.map((response) => response.status).sort()).toEqual([201, 409])
    expect(countAdmins(db)).toBe(1)
  })

  it("logs an admin in with a new account credential and returns their account from me", async () => {
    const { app, fixture } = buildApp()
    const setup = await setupAdmin(app, fixture)

    const response = await login(app)

    expect(response.status).toBe(200)
    const body = await response.json() as { user: { id: string; username: string }; token: string }
    expect(body.user).toMatchObject({ id: setup.user.id, username: "admin" })
    expect(body.token).not.toBe(setup.token)
    const me = await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${body.token}` } })
    expect(me.status).toBe(200)
    expect(await me.json()).toEqual({ user: expect.objectContaining({ id: setup.user.id, username: "admin" }) })
  })

  it("logs a registered password user in", async () => {
    const { app, db } = buildApp()
    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered",
      passwordHash: await hashPassword("long-password-for-test"),
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })

    const response = await login(app, { username: "registered" })

    expect(response.status).toBe(200)
    expect((await response.json() as { user: { type: string } }).user.type).toBe("registered")
  })

  it("gives invalid and unknown logins the same response and throttles repeated failures", async () => {
    const { app, fixture } = buildApp()
    await setupAdmin(app, fixture)

    const invalid = await login(app, { password: "incorrect-password" })
    const unknown = await login(app, { username: "nobody", password: "incorrect-password" })
    expect(invalid.status).toBe(401)
    expect(unknown.status).toBe(401)
    expect(await unknown.json()).toEqual(await invalid.json())

    for (let attempt = 0; attempt < 3; attempt += 1) {
      expect((await login(app, { username: "rate_user", password: "incorrect-password" })).status).toBe(401)
    }
    expect((await login(app, { username: "rate_user", password: "incorrect-password" })).status).toBe(429)
  })

  it("does not share source throttling when the source address is unavailable", async () => {
    let source: string | undefined = "127.0.0.1"
    const { app, fixture } = buildApp({ getClientAddress: () => source })
    await setupAdmin(app, fixture)
    source = undefined

    for (const username of ["unknown_one", "unknown_two", "unknown_three", "unknown_four", "unknown_five", "unknown_six"]) {
      expect((await login(app, { username, password: "incorrect-password" })).status).toBe(401)
    }
  })

  it("revokes only the current credential on logout", async () => {
    const { app, fixture } = buildApp()
    await setupAdmin(app, fixture)
    const first = await login(app, { deviceLabel: "first" })
    const second = await login(app, { deviceLabel: "second" })
    const firstToken = (await first.json() as { token: string }).token
    const secondToken = (await second.json() as { token: string }).token

    const logout = await app.request("/api/v1/auth/logout", {
      method: "POST",
      headers: { Authorization: `Bearer ${firstToken}` },
    })

    expect(logout.status).toBe(204)
    expect((await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${firstToken}` } })).status).toBe(401)
    expect((await app.request("/api/v1/auth/me", { headers: { Authorization: `Bearer ${secondToken}` } })).status).toBe(200)
  })

  it("revokes a caller's own account credential through the credential revoke route", async () => {
    const { app, authService, fixture } = buildApp()
    await setupAdmin(app, fixture)
    const loginResponse = await login(app)
    const token = (await loginResponse.json() as { token: string }).token
    const authenticated = await authService.authenticate(token)
    expect(authenticated).not.toBeNull()
    expect((await app.request(`/api/v1/auth/credentials/${authenticated!.credentialId}/revoke`, {
      method: "POST",
      headers: { Authorization: `Bearer ${token}` },
    })).status).toBe(204)
    expect((await app.request("/api/v1/auth/me", {
      headers: { Authorization: `Bearer ${token}` },
    })).status).toBe(401)
  })

  it("allows an admin account to ban and unban another account, but not a peer", async () => {
    const { app, authService, db, fixture } = buildApp()
    const owner = await setupAdmin(app, fixture)
    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const registeredToken = authService.issueAccountCredential("registered-user", "registered-device")

    expect((await app.request("/api/v1/admin/users/registered-user/ban", {
      method: "POST",
      headers: { Authorization: `Bearer ${owner.token}` },
    })).status).toBe(204)
    expect((await app.request("/api/v1/auth/me", {
      headers: { Authorization: `Bearer ${registeredToken}` },
    })).status).toBe(401)
    expect((await app.request("/api/v1/admin/users/registered-user/unban", {
      method: "POST",
      headers: { Authorization: `Bearer ${owner.token}` },
    })).status).toBe(204)
    expect((await app.request(`/api/v1/admin/users/${owner.user.id}/ban`, {
      method: "POST",
      headers: { Authorization: `Bearer ${registeredToken}` },
    })).status).toBe(403)
  })

  it("requires account scope for credential revocation and ban routes", async () => {
    const { app, authService, db, fixture } = buildApp()
    const owner = await setupAdmin(app, fixture)
    createInstance(db, {
      id: "instance-1",
      ownerId: owner.user.id,
      name: "phone-1",
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
      desiredState: "running",
      observedState: "running",
      runtimeHandle: null,
      stateDataRef: null,
      lastError: null,
    })
    createSession(db, { id: "session-1", instanceId: "instance-1", status: "active" })
    createAttachment(db, {
      id: "attachment-1",
      sessionId: "session-1",
      userId: owner.user.id,
      clientId: "attachment-client",
      role: "owner",
      status: "disconnected",
    })
    const attachmentToken = authService.issueAttachmentCredential({
      userId: owner.user.id,
      sessionId: "session-1",
      attachmentId: "attachment-1",
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
    })

    expect((await app.request("/api/v1/auth/credentials/does-not-exist/revoke", {
      method: "POST",
      headers: { Authorization: `Bearer ${attachmentToken}` },
    })).status).toBe(403)
    expect((await app.request(`/api/v1/admin/users/${owner.user.id}/ban`, {
      method: "POST",
      headers: { Authorization: `Bearer ${attachmentToken}` },
    })).status).toBe(403)
  })

  it("issues hashed registration invitations and atomically registers one password account", async () => {
    const { app, db, fixture } = buildApp()
    const admin = await setupAdmin(app, fixture)

    expect((await app.request("/api/v1/auth/register", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ username: "new_user", password: "long-password-for-test", deviceLabel: "phone" }),
    })).status).toBe(400)

    const invitation = await app.request("/api/v1/auth/registration-invites", {
      method: "POST",
      headers: { Authorization: `Bearer ${admin.token}`, "content-type": "application/json" },
      body: JSON.stringify({ ttlMs: 5 * 60_000 }),
    })
    expect(invitation.status).toBe(201)
    const { token } = await invitation.json() as { token: string }

    const register = (username: string) => app.request("/api/v1/auth/register", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ inviteToken: token, username, password: "long-password-for-test", deviceLabel: "phone" }),
    })
    const results = await Promise.all([register("New_User"), register("other_user")])
    expect(results.map((response) => response.status).sort()).toEqual([201, 400])
    const result = results.find((response) => response.status === 201)!
    expect(["new_user", "other_user"]).toContain((await result.json() as { user: { username: string }; token: string }).user.username)
    expect(listUsers(db).filter((user) => user.username != null)).toHaveLength(2)
  })

  it("allows a legacy credential to enroll only its authenticated user", async () => {
    const { app, authService, db } = buildApp()
    for (const id of ["legacy_one", "legacy_two"]) {
      createUser(db, {
        id,
        displayName: id,
        userType: "registered",
        username: null,
        passwordHash: null,
        enrollmentRequired: true,
        isServerOwner: false,
        status: "active",
      })
    }
    const token = authService.issueAccountCredential("legacy_one", "legacy-client")

    const response = await app.request("/api/v1/auth/enroll", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "content-type": "application/json" },
      body: JSON.stringify({ username: "legacy_one", password: "long-password-for-test" }),
    })
    expect(response.status).toBe(200)
    expect(getUserById(db, "legacy_one")).toMatchObject({ username: "legacy_one", enrollmentRequired: false })
    expect(getUserById(db, "legacy_two")).toMatchObject({ username: null, passwordHash: null, enrollmentRequired: true })
  })

  it("rejects legacy enrollment with an already-taken normalized username without changing either user", async () => {
    const { app, authService, db } = buildApp()
    createUser(db, {
      id: "legacy_user",
      displayName: "Legacy User",
      userType: "registered",
      username: null,
      passwordHash: null,
      enrollmentRequired: true,
      isServerOwner: false,
      status: "active",
    })
    createUser(db, {
      id: "taken_user",
      displayName: "Taken User",
      userType: "registered",
      username: "taken_name",
      passwordHash: await hashPassword("long-password-for-test"),
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const token = authService.issueAccountCredential("legacy_user", "legacy-client")

    const response = await app.request("/api/v1/auth/enroll", {
      method: "POST",
      headers: { Authorization: `Bearer ${token}`, "content-type": "application/json" },
      body: JSON.stringify({ username: "TAKEN_NAME", password: "long-password-for-test" }),
    })

    expect(response.status).toBe(400)
    expect(getUserById(db, "legacy_user")).toMatchObject({
      username: null,
      passwordHash: null,
      enrollmentRequired: true,
    })
    expect(getUserById(db, "taken_user")).toMatchObject({ username: "taken_name", enrollmentRequired: false })
  })

  it("retires passwordless pairing invitation routes", async () => {
    const { app } = buildApp()
    expect((await app.request("/api/v1/auth/pairing-invites", { method: "POST" })).status).toBe(410)
    expect((await app.request("/api/v1/auth/pairing-invites/redeem", { method: "POST" })).status).toBe(410)
  })

  it("does not trust forwarded loopback headers and retires the legacy claim route", async () => {
    const { app } = buildApp({ getClientAddress: () => "203.0.113.5" })

    const setup = await app.request("/api/v1/auth/setup", {
      method: "POST",
      headers: { "content-type": "application/json", "x-forwarded-for": "127.0.0.1" },
      body: JSON.stringify({
        username: "admin",
        password: "long-password-for-test",
        deviceLabel: "local",
        bootstrapSecret: "not-the-file-secret",
      }),
    })

    expect(setup.status).toBe(403)
    expect((await app.request("/api/v1/auth/claim", { method: "POST" })).status).toBe(404)
  })
})
