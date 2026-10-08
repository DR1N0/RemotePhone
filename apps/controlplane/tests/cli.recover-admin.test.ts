import { chmodSync, mkdtempSync, rmSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"

import { describe, expect, it } from "vitest"

import { assertLocalDatabaseOwnership, AdminRecoveryError, findActiveAdminForRecovery, recoverExistingAdmin } from "../src/cli/recover-admin.js"
import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { getCredentialById } from "../src/persistence/repositories/credentials.js"
import { createUser, getUserById } from "../src/persistence/repositories/users.js"
import { verifyPassword } from "../src/services/password.js"

describe("local administrator recovery", () => {
  it("rejects a guest, resets an existing admin, and revokes that admin's existing credentials atomically", async () => {
    resetTestDb()
    const db = createTestDb()
    createUser(db, { id: "admin", displayName: "Admin", userType: "admin", username: "admin", passwordHash: "old", enrollmentRequired: true, isServerOwner: true, status: "active" })
    createUser(db, { id: "guest", displayName: "Guest", userType: "guest", username: null, passwordHash: null, enrollmentRequired: true, isServerOwner: false, status: "active" })
    db.$client.prepare("INSERT INTO credentials (id, user_id, token_hash, device_label, scope) VALUES (?, ?, ?, ?, ?)").run("old-device", "admin", "old-hash", "phone", "account")

    await expect(recoverExistingAdmin(db, { adminId: "guest", username: "guest", password: "new-secure-password" }))
      .rejects.toBeInstanceOf(AdminRecoveryError)
    expect(getUserById(db, "guest")?.userType).toBe("guest")

    await recoverExistingAdmin(db, { adminId: "admin", username: "recovered", password: "new-secure-password", now: new Date("2026-10-02T00:00:00.000Z") })
    const admin = getUserById(db, "admin")
    expect(admin).toMatchObject({ username: "recovered", enrollmentRequired: false })
    expect(await verifyPassword("new-secure-password", admin?.passwordHash ?? "")).toBe(true)
    expect(getCredentialById(db, "old-device")?.revokedAt).toBe("2026-10-02T00:00:00.000Z")
  })

  it("finds a legacy active admin without a username by stable ID and restores login", async () => {
    resetTestDb()
    const db = createTestDb()
    createUser(db, { id: "legacy-admin", displayName: "Legacy", userType: "admin", username: null, passwordHash: null, enrollmentRequired: true, isServerOwner: true, status: "active" })

    expect(findActiveAdminForRecovery(db, "legacy-admin")?.id).toBe("legacy-admin")
    await recoverExistingAdmin(db, { adminId: "legacy-admin", username: "recovered", password: "new-secure-password" })
    expect(getUserById(db, "legacy-admin")).toMatchObject({ username: "recovered", enrollmentRequired: false })
  })

  it("rejects a banned administrator without changing its password or credentials", async () => {
    resetTestDb()
    const db = createTestDb()
    createUser(db, { id: "banned-admin", displayName: "Banned Admin", userType: "admin", username: "banned", passwordHash: "old-hash", enrollmentRequired: true, isServerOwner: true, status: "banned" })
    db.$client.prepare("INSERT INTO credentials (id, user_id, token_hash, device_label, scope) VALUES (?, ?, ?, ?, ?)").run("banned-device", "banned-admin", "old-hash", "phone", "account")

    await expect(recoverExistingAdmin(db, { adminId: "banned-admin", username: "recovered", password: "new-secure-password" }))
      .rejects.toBeInstanceOf(AdminRecoveryError)
    expect(getUserById(db, "banned-admin")).toMatchObject({ username: "banned", passwordHash: "old-hash", enrollmentRequired: true, status: "banned" })
    expect(getCredentialById(db, "banned-device")?.revokedAt).toBeNull()
  })

  it("requires a non-world-writable local database file", () => {
    const directory = mkdtempSync(join(tmpdir(), "mako-recovery-"))
    const databasePath = join(directory, "mako.db")
    try {
      writeFileSync(databasePath, "")
      chmodSync(databasePath, 0o666)
      expect(() => assertLocalDatabaseOwnership(databasePath)).toThrow("must not be group- or world-writable")
    } finally {
      rmSync(directory, { recursive: true, force: true })
    }
  })
})
