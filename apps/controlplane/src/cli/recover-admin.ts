import { statSync } from "node:fs"
import process from "node:process"
import { createInterface } from "node:readline/promises"

import { and, eq, isNull, sql } from "drizzle-orm"

import { createAppDb, type AppDb } from "../persistence/db.js"
import { credentials, users } from "../persistence/schema.js"
import { getUserByUsername, normalizeUsername } from "../persistence/repositories/users.js"
import { hashPassword } from "../services/password.js"

export class AdminRecoveryError extends Error {}

export function assertLocalDatabaseOwnership(databasePath: string): void {
  const stat = statSync(databasePath)
  if (!stat.isFile()) throw new AdminRecoveryError("recovery database path is not a regular file")
  if (typeof process.getuid === "function" && stat.uid !== process.getuid()) {
    throw new AdminRecoveryError("recovery database is not owned by the local operator")
  }
  if ((stat.mode & 0o022) !== 0) {
    throw new AdminRecoveryError("recovery database must not be group- or world-writable")
  }
}

export function findActiveAdminForRecovery(db: AppDb, identifier: string) {
  const byId = db.select().from(users).where(eq(users.id, identifier.trim())).get()
  const target = byId ?? getUserByUsername(db, identifier)
  return target?.userType === "admin" && target.status === "active" ? target : undefined
}

export async function recoverExistingAdmin(
  db: AppDb,
  input: { adminId: string; username: string; password: string; now?: Date },
): Promise<void> {
  const username = normalizeUsername(input.username)
  if (!/^[a-z0-9_]{3,32}$/.test(username)) throw new AdminRecoveryError("invalid recovery username")
  if (input.password.length < 12 || input.password.length > 1024) throw new AdminRecoveryError("invalid recovery password")

  const passwordHash = await hashPassword(input.password)
  const recoveredAt = (input.now ?? new Date()).toISOString()
  const transaction = db.$client.transaction(() => {
    const target = db.select().from(users).where(eq(users.id, input.adminId)).get()
    if (target == null || target.userType !== "admin" || target.status !== "active") {
      throw new AdminRecoveryError("recovery target must be an existing active administrator")
    }
    const usernameOwner = getUserByUsername(db, username)
    if (usernameOwner != null && usernameOwner.id !== target.id) {
      throw new AdminRecoveryError("recovery username is already in use")
    }

    db.update(users).set({
      username,
      passwordHash,
      enrollmentRequired: false,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    }).where(eq(users.id, target.id)).run()
    db.update(credentials).set({
      revokedAt: recoveredAt,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    }).where(and(eq(credentials.userId, target.id), isNull(credentials.revokedAt))).run()
  })
  transaction.immediate()
}

async function readSecret(prompt: string): Promise<string> {
  if (!process.stdin.isTTY || !process.stdout.isTTY) {
    throw new AdminRecoveryError("admin recovery requires a local interactive terminal")
  }
  process.stdout.write(prompt)
  process.stdin.setRawMode(true)
  process.stdin.resume()
  return await new Promise((resolve, reject) => {
    let value = ""
    const finish = (error?: Error) => {
      process.stdin.off("data", onData)
      process.stdin.setRawMode(false)
      process.stdout.write("\n")
      if (error != null) reject(error)
      else resolve(value)
    }
    const onData = (chunk: Buffer) => {
      for (const character of chunk.toString("utf8")) {
        if (character === "\r" || character === "\n") return finish()
        if (character === "") return finish(new AdminRecoveryError("admin recovery cancelled"))
        if (character === "" || character === "\b") value = value.slice(0, -1)
        else value += character
      }
    }
    process.stdin.on("data", onData)
  })
}

export async function runRecoverAdminCli(databasePath = process.env.MAKO_CONTROLPLANE_DB_PATH ?? "./data/mako.db"): Promise<void> {
  assertLocalDatabaseOwnership(databasePath)
  if (!process.stdin.isTTY || !process.stdout.isTTY) {
    throw new AdminRecoveryError("admin recovery requires a local interactive terminal")
  }
  const db = createAppDb(databasePath)
  const readline = createInterface({ input: process.stdin, output: process.stdout })
  try {
    const identifier = await readline.question("Existing active admin ID or username: ")
    const admin = findActiveAdminForRecovery(db, identifier)
    if (admin == null) {
      throw new AdminRecoveryError("recovery target must be an existing active administrator")
    }
    const username = await readline.question("New admin username: ")
    readline.close()
    const password = await readSecret("New admin password: ")
    await recoverExistingAdmin(db, { adminId: admin.id, username, password })
    process.stdout.write("Administrator recovery complete. Log in locally through the normal login endpoint.\n")
  } finally {
    readline.close()
    db.$client.close()
  }
}

if (process.argv[1]?.endsWith("recover-admin.js") || process.argv[1]?.endsWith("recover-admin.ts")) {
  void runRecoverAdminCli().catch((error: unknown) => {
    process.stderr.write(`${error instanceof Error ? error.message : "admin recovery failed"}\n`)
    process.exitCode = 1
  })
}
