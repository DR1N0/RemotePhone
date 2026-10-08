import { and, eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { credentials, type CredentialRow, type NewCredentialRow } from "../schema.js"

export function createCredential(db: AppDb, row: NewCredentialRow): void {
  db.insert(credentials).values(row).run()
}

export function getCredentialByTokenHash(db: AppDb, hash: string): CredentialRow | undefined {
  return db.select().from(credentials).where(eq(credentials.tokenHash, hash)).get()
}

export function getCredentialById(db: AppDb, id: string): CredentialRow | undefined {
  return db.select().from(credentials).where(eq(credentials.id, id)).get()
}

export function listAccountCredentialsForUser(db: AppDb, userId: string): CredentialRow[] {
  return db.select().from(credentials)
    .where(and(eq(credentials.userId, userId), eq(credentials.scope, "account")))
    .all()
}

export function getAccountCredentialForUser(
  db: AppDb,
  input: { userId: string; credentialId: string },
): CredentialRow | undefined {
  return db.select().from(credentials)
    .where(and(
      eq(credentials.id, input.credentialId),
      eq(credentials.userId, input.userId),
      eq(credentials.scope, "account"),
    ))
    .get()
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

export function revokeAttachmentCredentials(
  db: AppDb,
  input: { sessionId: string; attachmentId: string; revokedAt: string },
): void {
  db.update(credentials)
    .set({
      revokedAt: input.revokedAt,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(and(
      eq(credentials.scope, "attachment"),
      eq(credentials.sessionId, input.sessionId),
      eq(credentials.attachmentId, input.attachmentId),
    ))
    .run()
}
