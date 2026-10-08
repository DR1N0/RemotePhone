import { readFileSync } from "node:fs"
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"

const { passwordHashSpy } = vi.hoisted(() => ({ passwordHashSpy: vi.fn() }))

vi.mock("../src/services/password.js", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../src/services/password.js")>()
  return {
    ...actual,
    hashPassword: async (password: string) => {
      passwordHashSpy(password)
      return await actual.hashPassword(password)
    },
  }
})

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createAttachment } from "../src/persistence/repositories/attachments.js"
import { createCredential, getCredentialByTokenHash } from "../src/persistence/repositories/credentials.js"
import { createInstance } from "../src/persistence/repositories/instances.js"
import { closeSession, createSession } from "../src/persistence/repositories/sessions.js"
import { setUserStatus } from "../src/persistence/repositories/users.js"
import { createUser, listUsers } from "../src/persistence/repositories/users.js"
import {
  AuthServiceError,
  createAuthService,
  InvalidLoginError,
  RegistrationInviteInvalidError,
  ServerAlreadyClaimedError,
} from "../src/services/auth-service.js"
import { hashPassword } from "../src/services/password.js"
import { hashToken } from "../src/services/token.js"
import { createAdminAuthFixture } from "./helpers/admin-auth.js"

const fixtures: Array<{ cleanup: () => void }> = []

function createService(now?: () => Date) {
  const db = createTestDb()
  const fixture = createAdminAuthFixture()
  fixtures.push(fixture)
  const authService = createAuthService({ db, now, bootstrapSecretPath: fixture.bootstrapSecretPath })
  return { db, fixture, authService }
}

async function setupAdmin(
  authService: ReturnType<typeof createAuthService>,
  fixture: { bootstrapSecretPath: string },
  input: { username?: string; password?: string; deviceLabel?: string } = {},
) {
  return await authService.setupAdmin({
    username: input.username ?? "admin",
    password: input.password ?? "long-password-for-test",
    deviceLabel: input.deviceLabel ?? "test-device",
    bootstrapSecret: readFileSync(fixture.bootstrapSecretPath, "utf8"),
  })
}

function seedAttachmentTarget(db: ReturnType<typeof createTestDb>, userId: string): void {
  createInstance(db, {
    id: "instance-1",
    ownerId: userId,
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
    userId: userId,
    clientId: "client-1",
    role: "owner",
    status: "disconnected",
  })
}

describe("auth service — account setup and login", () => {
  beforeEach(() => {
    resetTestDb()
    passwordHashSpy.mockClear()
  })

  afterEach(() => {
    for (const fixture of fixtures.splice(0)) {
      fixture.cleanup()
    }
  })

  it("sets up an unclaimed server once and invalidates the secret after its transaction commits", async () => {
    const { authService, fixture } = createService()
    expect(authService.getSetupStatus()).toEqual({ claimed: false })

    const setup = await setupAdmin(authService, fixture)

    expect(setup.user).toMatchObject({ type: "admin", username: "admin" })
    expect(await authService.authenticate(setup.token)).toMatchObject({ userId: setup.user.id, scope: "account" })
    expect(authService.getSetupStatus()).toEqual({ claimed: true })
    await expect(authService.setupAdmin({
      username: "other_admin",
      password: "long-password-for-test",
      deviceLabel: "test-device",
      bootstrapSecret: "unused-after-claim",
    })).rejects.toThrow(ServerAlreadyClaimedError)
  })

  it("does not make bootstrap setup eligible when registered users exist, but ignores guest history", async () => {
    const { authService, fixture, db } = createService()
    createUser(db, { id: "guest-history", displayName: "Guest", userType: "guest", username: null, passwordHash: null, enrollmentRequired: false, isServerOwner: false, status: "active" })
    expect(authService.getSetupStatus()).toEqual({ claimed: false })
    createUser(db, { id: "registered-history", displayName: "Registered", userType: "registered", username: "registered_history", passwordHash: null, enrollmentRequired: true, isServerOwner: false, status: "banned" })
    expect(authService.getSetupStatus()).toEqual({ claimed: true })
    await expect(setupAdmin(authService, fixture)).rejects.toThrow(ServerAlreadyClaimedError)
  })

  it("issues a new 30-day account token after verifying a password login", async () => {
    const { authService, fixture, db } = createService()
    const setup = await setupAdmin(authService, fixture)
    const login = await authService.login({
      username: "ADMIN",
      password: "long-password-for-test",
      deviceLabel: "laptop",
    })

    expect(login.user).toEqual(setup.user)
    expect(login.token).not.toBe(setup.token)
    expect(getCredentialByTokenHash(db, hashToken(login.token))).toMatchObject({
      scope: "account",
      expiresAt: expect.any(String),
    })
  })

  it("rejects unknown, banned, unenrolled, and invalid-password users with the same login error", async () => {
    const { authService, db } = createService()
    createUser(db, {
      id: "registered",
      displayName: "Registered",
      userType: "registered",
      username: "registered",
      passwordHash: await hashPassword("long-password-for-test"),
      enrollmentRequired: true,
      isServerOwner: false,
      status: "active",
    })

    for (const input of [
      { username: "unknown", password: "long-password-for-test" },
      { username: "registered", password: "incorrect-password" },
      { username: "registered", password: "long-password-for-test" },
    ]) {
      await expect(authService.login({ ...input, deviceLabel: "laptop" })).rejects.toThrow(InvalidLoginError)
    }
  })

  it("does not leave an admin when setup gets an invalid secret", async () => {
    const { authService, db } = createService()
    await expect(authService.setupAdmin({
      username: "admin",
      password: "long-password-for-test",
      deviceLabel: "local",
      bootstrapSecret: "wrong-secret",
    })).rejects.toThrow()
    expect(listUsers(db)).toEqual([])
  })

  it("revokes only the named current credential", async () => {
    const { authService, fixture } = createService()
    await setupAdmin(authService, fixture)
    const first = await authService.login({ username: "admin", password: "long-password-for-test", deviceLabel: "first" })
    const second = await authService.login({ username: "admin", password: "long-password-for-test", deviceLabel: "second" })
    const firstAuthenticated = await authService.authenticate(first.token)

    await authService.revokeCurrentCredential(firstAuthenticated!.credentialId)

    expect(await authService.authenticate(first.token)).toBeNull()
    expect(await authService.authenticate(second.token)).not.toBeNull()
  })
})

describe("auth service — scoped credentials", () => {
  const currentTime = new Date("2026-10-02T12:00:00.000Z")

  beforeEach(() => {
    resetTestDb()
    passwordHashSpy.mockClear()
  })

  afterEach(() => {
    for (const fixture of fixtures.splice(0)) {
      fixture.cleanup()
    }
  })

  it("issues distinct, hashed account and attachment credentials with their scopes", async () => {
    const db = createTestDb()
    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    seedAttachmentTarget(db, "registered-user")
    const authService = createAuthService({ db, now: () => currentTime })

    const accountToken = authService.issueAccountCredential("registered-user", "laptop")
    const attachmentToken = authService.issueAttachmentCredential({
      userId: "registered-user",
      sessionId: "session-1",
      attachmentId: "attachment-1",
      expiresAt: "2026-10-02T13:00:00.000Z",
    })

    expect(getCredentialByTokenHash(db, hashToken(accountToken))).toMatchObject({
      scope: "account",
      sessionId: null,
      attachmentId: null,
      expiresAt: "2026-11-01T12:00:00.000Z",
    })
    expect(getCredentialByTokenHash(db, hashToken(attachmentToken))).toMatchObject({
      scope: "attachment",
      sessionId: "session-1",
      attachmentId: "attachment-1",
    })
    await expect(authService.authenticate(attachmentToken)).resolves.toMatchObject({
      scope: "attachment",
      sessionId: "session-1",
      attachmentId: "attachment-1",
    })
  })

  it("rejects revoked, banned, not-yet-valid, expired, and guest account credentials", async () => {
    const db = createTestDb()
    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    createUser(db, {
      id: "guest-user",
      displayName: "Guest User",
      userType: "guest",
      username: null,
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    const authService = createAuthService({ db, now: () => currentTime })
    const revokedToken = authService.issueAccountCredential("registered-user", "revoked")
    const revoked = await authService.authenticate(revokedToken)
    await authService.revokeCurrentCredential(revoked!.credentialId)
    expect(await authService.authenticate(revokedToken)).toBeNull()

    seedAttachmentTarget(db, "registered-user")
    const bannedToken = authService.issueAccountCredential("registered-user", "banned")
    const bannedAttachmentToken = authService.issueAttachmentCredential({
      userId: "registered-user",
      sessionId: "session-1",
      attachmentId: "attachment-1",
      expiresAt: "2026-10-02T13:00:00.000Z",
    })
    setUserStatus(db, { id: "registered-user", status: "banned" })
    expect(await authService.authenticate(bannedToken)).toBeNull()
    expect(await authService.authenticate(bannedAttachmentToken)).toBeNull()

    createCredential(db, {
      id: "future-credential",
      userId: "guest-user",
      tokenHash: hashToken("future-token"),
      deviceLabel: "future",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: "2026-10-02T12:00:01.000Z",
      expiresAt: null,
      revokedAt: null,
    })
    createCredential(db, {
      id: "expired-credential",
      userId: "guest-user",
      tokenHash: hashToken("expired-token"),
      deviceLabel: "expired",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: "2026-10-02T11:59:59.000Z",
      revokedAt: null,
    })
    createCredential(db, {
      id: "guest-account-credential",
      userId: "guest-user",
      tokenHash: hashToken("guest-account-token"),
      deviceLabel: "guest",
      scope: "account",
      sessionId: null,
      attachmentId: null,
      notBefore: null,
      expiresAt: null,
      revokedAt: null,
    })
    expect(await authService.authenticate("future-token")).toBeNull()
    expect(await authService.authenticate("expired-token")).toBeNull()
    expect(await authService.authenticate("guest-account-token")).toBeNull()
  })

  it("enforces the attachment expiry cap and binding to an active session", () => {
    const db = createTestDb()
    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    createUser(db, {
      id: "other-user",
      displayName: "Other User",
      userType: "registered",
      username: "other",
      enrollmentRequired: false,
      isServerOwner: false,
      status: "active",
    })
    seedAttachmentTarget(db, "registered-user")
    createAttachment(db, {
      id: "other-attachment",
      sessionId: "session-1",
      userId: "other-user",
      clientId: "other-client",
      role: "participant",
      status: "disconnected",
    })
    const authService = createAuthService({ db, now: () => currentTime })

    expect(() => authService.issueAttachmentCredential({
      userId: "registered-user",
      sessionId: "session-1",
      attachmentId: "attachment-1",
      expiresAt: "2026-10-03T12:00:00.001Z",
    })).toThrow(AuthServiceError)
    expect(() => authService.issueAttachmentCredential({
      userId: "registered-user",
      sessionId: "session-1",
      attachmentId: "other-attachment",
      expiresAt: "2026-10-02T13:00:00.000Z",
    })).toThrow(AuthServiceError)

    const token = authService.issueAttachmentCredential({
      userId: "registered-user",
      sessionId: "session-1",
      attachmentId: "attachment-1",
      expiresAt: "2026-10-02T13:00:00.000Z",
    })
    closeSession(db, { id: "session-1" })
    return expect(authService.authenticate(token)).resolves.toBeNull()
  })

  it("registers a password account once from a hashed expiring registration invitation", async () => {
    const { authService, fixture } = createService()
    const admin = await setupAdmin(authService, fixture)
    const invite = await authService.createRegistrationInvite({ actorUserId: admin.user.id, actorCredentialId: null, requestId: "request-registration-invite" })
    const registered = await authService.register({
      inviteToken: invite.token,
      username: "New_User",
      password: "long-password-for-test",
      deviceLabel: "phone",
      requestId: "request-registration-redeem",
    })

    expect(registered.user).toMatchObject({ type: "registered", username: "new_user" })
    expect(await authService.authenticate(registered.token)).toMatchObject({ userId: registered.user.id, scope: "account" })
    await expect(authService.register({
      inviteToken: invite.token,
      username: "second_user",
      password: "long-password-for-test",
      deviceLabel: "phone",
      requestId: "request-registration-redeem",
    })).rejects.toThrow(RegistrationInviteInvalidError)
  })

  it("rejects an expired registration invitation without creating a user", async () => {
    let current = new Date("2026-10-02T12:00:00.000Z")
    const { authService, fixture, db } = createService(() => current)
    const admin = await setupAdmin(authService, fixture)
    const invite = await authService.createRegistrationInvite({
      actorUserId: admin.user.id,
      actorCredentialId: null,
      requestId: "request-registration-invite-ttl",
      ttlMs: 5 * 60_000,
    })
    current = new Date("2026-10-02T12:05:00.000Z")

    await expect(authService.register({
      inviteToken: invite.token,
      username: "expired_user",
      password: "long-password-for-test",
      deviceLabel: "phone",
      requestId: "request-registration-redeem",
    })).rejects.toThrow(RegistrationInviteInvalidError)
    expect(listUsers(db)).toHaveLength(1)
  })

  it("rejects malformed, unknown, and expired invitation tokens before hashing a password", async () => {
    let current = new Date("2026-10-02T12:00:00.000Z")
    const { authService, fixture } = createService(() => current)
    const admin = await setupAdmin(authService, fixture)
    passwordHashSpy.mockClear()

    await expect(authService.register({
      inviteToken: "malformed",
      username: "valid_user",
      password: "long-password-for-test",
      deviceLabel: "phone",
      requestId: "request-registration-redeem",
    })).rejects.toThrow(RegistrationInviteInvalidError)
    await expect(authService.register({
      inviteToken: "unknown-token",
      username: "valid_user",
      password: "long-password-for-test",
      deviceLabel: "phone",
      requestId: "request-registration-redeem",
    })).rejects.toThrow(RegistrationInviteInvalidError)
    expect(passwordHashSpy).not.toHaveBeenCalled()

    const invite = await authService.createRegistrationInvite({
      actorUserId: admin.user.id,
      actorCredentialId: null,
      requestId: "request-registration-invite-ttl",
      ttlMs: 5 * 60_000,
    })
    current = new Date("2026-10-02T12:05:00.000Z")
    await expect(authService.register({
      inviteToken: invite.token,
      username: "valid_user",
      password: "long-password-for-test",
      deviceLabel: "phone",
      requestId: "request-registration-redeem",
    })).rejects.toThrow(RegistrationInviteInvalidError)
    expect(passwordHashSpy).not.toHaveBeenCalled()
  })
})
