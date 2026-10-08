import { chmodSync, mkdtempSync, readFileSync, rmSync, statSync, symlinkSync, writeFileSync } from "node:fs"
import { tmpdir } from "node:os"
import { join } from "node:path"
import { afterEach, describe, expect, it } from "vitest"

import { createBootstrapSecretService } from "../src/services/bootstrap-secret.js"

const temporaryDirectories: string[] = []

function createSecretPath(): string {
  const directory = mkdtempSync(join(tmpdir(), "mako-bootstrap-secret-"))
  temporaryDirectories.push(directory)
  return join(directory, "bootstrap-secret")
}

afterEach(() => {
  for (const directory of temporaryDirectories.splice(0)) {
    rmSync(directory, { recursive: true, force: true })
  }
})

describe("bootstrap secret service", () => {
  it("creates one persistent 32-byte secret in an owner-only file", () => {
    const path = createSecretPath()
    const service = createBootstrapSecretService(path)

    const secret = service.getOrCreate()

    expect(Buffer.from(secret, "base64url")).toHaveLength(32)
    expect(readFileSync(path, "utf8")).toBe(secret)
    expect(statSync(path).mode & 0o777).toBe(0o600)
    expect(service.getOrCreate()).toBe(secret)
  })

  it("rejects an existing secret file that is malformed or not owner-only", () => {
    const malformedPath = createSecretPath()
    writeFileSync(malformedPath, "short-secret", { mode: 0o600 })
    expect(() => createBootstrapSecretService(malformedPath).getOrCreate()).toThrow(/bootstrap secret/i)

    const worldReadablePath = createSecretPath()
    const generated = createBootstrapSecretService(worldReadablePath).getOrCreate()
    chmodSync(worldReadablePath, 0o644)
    expect(() => createBootstrapSecretService(worldReadablePath).getOrCreate()).toThrow(/bootstrap secret/i)
    expect(readFileSync(worldReadablePath, "utf8")).toBe(generated)
  })

  it("rejects an existing bootstrap-secret symlink", () => {
    const targetPath = createSecretPath()
    const secret = createBootstrapSecretService(targetPath).getOrCreate()
    const linkPath = createSecretPath()
    symlinkSync(targetPath, linkPath)

    expect(() => createBootstrapSecretService(linkPath).getOrCreate()).toThrow(/bootstrap secret/i)
    expect(readFileSync(targetPath, "utf8")).toBe(secret)
  })

  it("compares safely and only invalidates the matching secret", () => {
    const path = createSecretPath()
    const service = createBootstrapSecretService(path)
    const secret = service.getOrCreate()

    expect(service.matches("wrong-secret")).toBe(false)
    service.invalidate("wrong-secret")
    expect(readFileSync(path, "utf8")).toBe(secret)

    service.invalidate(secret)
    expect(() => readFileSync(path, "utf8")).toThrow()
  })
})
