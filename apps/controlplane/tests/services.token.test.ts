import { describe, expect, it } from "vitest"

import { generateToken, hashToken } from "../src/services/token.js"

describe("token utility", () => {
  it("generates high-entropy, URL-safe tokens", () => {
    const a = generateToken()
    const b = generateToken()

    expect(a).not.toBe(b)
    expect(a.length).toBeGreaterThanOrEqual(32)
    expect(a).toMatch(/^[A-Za-z0-9_-]+$/)
  })

  it("hashes deterministically and does not return the raw token", () => {
    const token = generateToken()
    const hashed = hashToken(token)

    expect(hashed).not.toBe(token)
    expect(hashed).toBe(hashToken(token))
    expect(hashed).toMatch(/^[0-9a-f]{64}$/)
  })
})
