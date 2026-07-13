import { existsSync, mkdirSync } from "node:fs"
import { dirname, resolve } from "node:path"
import { fileURLToPath } from "node:url"

import Database from "better-sqlite3"
import { drizzle, type BetterSQLite3Database } from "drizzle-orm/better-sqlite3"
import { migrate } from "drizzle-orm/better-sqlite3/migrator"

import { appSchema } from "./schema.js"

export type AppDb = BetterSQLite3Database<typeof appSchema> & {
  $client: Database.Database
}

const moduleDir = dirname(fileURLToPath(import.meta.url))
const migrationFolderCandidates = [...new Set([
  resolve(moduleDir, "migrations"),
  resolve(moduleDir, "../../src/persistence/migrations"),
])]

function resolveMigrationsFolder(): string {
  for (const candidate of migrationFolderCandidates) {
    if (existsSync(candidate)) {
      return candidate
    }
  }

  throw new Error(
    `Unable to locate Drizzle migrations. Looked in: ${migrationFolderCandidates.join(", ")}`,
  )
}

function connect(sqlite: Database.Database): AppDb {
  sqlite.pragma("foreign_keys = ON")

  const db = drizzle(sqlite, { schema: appSchema })
  migrate(db, { migrationsFolder: resolveMigrationsFolder() })
  return db
}

function createInMemoryDb(): AppDb {
  return connect(new Database(":memory:"))
}

let testDb = createInMemoryDb()

export function createAppDb(path: string): AppDb {
  mkdirSync(dirname(path), { recursive: true })
  return connect(new Database(path))
}

export function createTestDb(): AppDb {
  return testDb
}

export function resetTestDb(): void {
  testDb.$client.close()
  testDb = createInMemoryDb()
}
