import { randomBytes, timingSafeEqual } from "node:crypto"
import { chmodSync, closeSync, lstatSync, mkdirSync, openSync, readFileSync, unlinkSync, writeSync } from "node:fs"
import { dirname } from "node:path"

const BOOTSTRAP_SECRET_BYTES = 32
const BOOTSTRAP_SECRET_FILE_MODE = 0o600
const BOOTSTRAP_SECRET_PATTERN = /^[A-Za-z0-9_-]{43}$/

function invalidBootstrapSecretFile(): Error {
  return new Error("invalid bootstrap secret file")
}

function secretsMatch(actual: string, expected: string): boolean {
  const actualBytes = Buffer.from(actual)
  const expectedBytes = Buffer.from(expected)
  return actualBytes.length === expectedBytes.length && timingSafeEqual(actualBytes, expectedBytes)
}

export function createBootstrapSecretService(path: string) {
  function readExistingSecret(): string {
    const stats = lstatSync(path)
    if (!stats.isFile() || (stats.mode & 0o777) !== BOOTSTRAP_SECRET_FILE_MODE) {
      throw invalidBootstrapSecretFile()
    }

    const secret = readFileSync(path, "utf8")
    if (!BOOTSTRAP_SECRET_PATTERN.test(secret)) {
      throw invalidBootstrapSecretFile()
    }

    const decoded = Buffer.from(secret, "base64url")
    if (decoded.length !== BOOTSTRAP_SECRET_BYTES || decoded.toString("base64url") !== secret) {
      throw invalidBootstrapSecretFile()
    }
    return secret
  }

  function getOrCreate(): string {
    try {
      return readExistingSecret()
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
        throw error
      }
    }

    mkdirSync(dirname(path), { recursive: true, mode: 0o700 })
    const secret = randomBytes(BOOTSTRAP_SECRET_BYTES).toString("base64url")
    let descriptor: number | undefined
    try {
      descriptor = openSync(path, "wx", BOOTSTRAP_SECRET_FILE_MODE)
      writeSync(descriptor, secret)
      chmodSync(path, BOOTSTRAP_SECRET_FILE_MODE)
      return secret
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "EEXIST") {
        throw error
      }
      return readExistingSecret()
    } finally {
      if (descriptor != null) {
        closeSync(descriptor)
      }
    }
  }

  function matches(candidate: string): boolean {
    return secretsMatch(candidate, getOrCreate())
  }

  function invalidate(secret: string): void {
    try {
      if (secretsMatch(readExistingSecret(), secret)) {
        unlinkSync(path)
      }
    } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
        throw error
      }
    }
  }

  return { getOrCreate, matches, invalidate }
}
