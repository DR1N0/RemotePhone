import { randomBytes, scrypt, timingSafeEqual } from "node:crypto"
const ALGORITHM_VERSION = "scrypt-v1"
const COST = 16_384
const BLOCK_SIZE = 8
const PARALLELIZATION = 1
const KEY_LENGTH = 64
const SALT_LENGTH = 16
const MAX_MEMORY = 64 * 1024 * 1024

type ScryptParameters = {
  cost: number
  blockSize: number
  parallelization: number
  keyLength: number
}

function encodeHash(parameters: ScryptParameters, salt: Buffer, key: Buffer): string {
  return [
    ALGORITHM_VERSION,
    parameters.cost,
    parameters.blockSize,
    parameters.parallelization,
    parameters.keyLength,
    salt.toString("base64url"),
    key.toString("base64url"),
  ].join("$")
}

function parsePositiveInteger(value: string): number | null {
  if (!/^\d+$/.test(value)) {
    return null
  }

  const parsed = Number(value)
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : null
}

function parseEncodedHash(encoded: string): { parameters: ScryptParameters; salt: Buffer; key: Buffer } | null {
  const [version, costValue, blockSizeValue, parallelizationValue, keyLengthValue, saltValue, keyValue, ...extra] = encoded.split("$")
  if (
    version !== ALGORITHM_VERSION
    || costValue == null
    || blockSizeValue == null
    || parallelizationValue == null
    || keyLengthValue == null
    || saltValue == null
    || keyValue == null
    || extra.length > 0
  ) {
    return null
  }

  const cost = parsePositiveInteger(costValue)
  const blockSize = parsePositiveInteger(blockSizeValue)
  const parallelization = parsePositiveInteger(parallelizationValue)
  const keyLength = parsePositiveInteger(keyLengthValue)
  if (
    cost == null
    || blockSize == null
    || parallelization == null
    || keyLength == null
    || cost > COST
    || blockSize > BLOCK_SIZE
    || parallelization > PARALLELIZATION
    || keyLength > KEY_LENGTH
  ) {
    return null
  }

  try {
    const salt = Buffer.from(saltValue, "base64url")
    const key = Buffer.from(keyValue, "base64url")
    if (salt.length !== SALT_LENGTH || key.length !== keyLength) {
      return null
    }

    return { parameters: { cost, blockSize, parallelization, keyLength }, salt, key }
  } catch {
    return null
  }
}

function deriveKey(password: string, salt: Buffer, parameters: ScryptParameters): Promise<Buffer> {
  return new Promise((resolve, reject) => {
    scrypt(password, salt, parameters.keyLength, {
      N: parameters.cost,
      r: parameters.blockSize,
      p: parameters.parallelization,
      maxmem: MAX_MEMORY,
    }, (error, derivedKey) => {
      if (error != null) {
        reject(error)
        return
      }
      resolve(derivedKey)
    })
  })
}

export async function hashPassword(password: string): Promise<string> {
  const salt = randomBytes(SALT_LENGTH)
  const key = await deriveKey(password, salt, {
    cost: COST,
    blockSize: BLOCK_SIZE,
    parallelization: PARALLELIZATION,
    keyLength: KEY_LENGTH,
  })

  return encodeHash({
    cost: COST,
    blockSize: BLOCK_SIZE,
    parallelization: PARALLELIZATION,
    keyLength: KEY_LENGTH,
  }, salt, key)
}

export async function verifyPassword(password: string, encoded: string): Promise<boolean> {
  const parsed = parseEncodedHash(encoded)
  if (parsed == null) {
    return false
  }

  try {
    const derivedKey = await deriveKey(password, parsed.salt, parsed.parameters)
    return derivedKey.length === parsed.key.length && timingSafeEqual(derivedKey, parsed.key)
  } catch {
    return false
  }
}
