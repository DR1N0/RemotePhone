import { eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { credentials, type CredentialRow, type NewCredentialRow } from "../schema.js"

export function createCredential(db: AppDb, row: NewCredentialRow): void {
  db.insert(credentials).values(row).run()
}

export function getCredentialByTokenHash(db: AppDb, tokenHash: string): CredentialRow | undefined {
  return db.select().from(credentials).where(eq(credentials.tokenHash, tokenHash)).get()
}

export function getCredentialById(db: AppDb, id: string): CredentialRow | undefined {
  return db.select().from(credentials).where(eq(credentials.id, id)).get()
}

export function revokeCredential(
  db: AppDb,
  input: { id: string; revokedAt: string },
): void {
  db.update(credentials)
    .set({
      revokedAt: input.revokedAt,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(credentials.id, input.id))
    .run()
}
