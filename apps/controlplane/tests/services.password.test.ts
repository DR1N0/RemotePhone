import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { createUser, getUserById } from "../src/persistence/repositories/users.js"
import { hashPassword, verifyPassword } from "../src/services/password.js"

describe("password service", () => {
  beforeEach(() => {
    resetTestDb()
  })

  it("salts parameterized scrypt hashes and verifies only the matching password", async () => {
    const password = "long-password-for-test"

    const first = await hashPassword(password)
    const second = await hashPassword(password)

    expect(first).not.toBe(second)
    expect(first).toMatch(/^scrypt-v1\$\d+\$\d+\$\d+\$\d+\$[^$]+\$[^$]+$/)
    await expect(verifyPassword(password, first)).resolves.toBe(true)
    await expect(verifyPassword("wrong-password", first)).resolves.toBe(false)
    await expect(verifyPassword(password, "not-a-scrypt-password")).resolves.toBe(false)
  })

  it("persists only a password hash, never the plaintext password", async () => {
    const db = createTestDb()
    const password = "long-password-for-test"
    const passwordHash = await hashPassword(password)

    createUser(db, {
      id: "registered-user",
      displayName: "Registered User",
      userType: "registered",
      username: "registered",
      passwordHash,
      isServerOwner: false,
      status: "active",
    })

    const persisted = getUserById(db, "registered-user")
    expect(persisted?.passwordHash).toBe(passwordHash)
    expect(persisted?.passwordHash).not.toContain(password)
  })
})
