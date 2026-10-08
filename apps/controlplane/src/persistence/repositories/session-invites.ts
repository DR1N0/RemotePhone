import { and, eq, gt, isNull } from "drizzle-orm"

import type { AppDb } from "../db.js"
import {
  sessionInvites,
  type NewSessionInviteRow,
  type SessionInviteRow,
} from "../schema.js"

export function createSessionInvite(db: AppDb, row: NewSessionInviteRow): void {
  db.insert(sessionInvites).values(row).run()
}

export function getSessionInviteByTokenHash(db: AppDb, tokenHash: string): SessionInviteRow | undefined {
  return db.select().from(sessionInvites).where(eq(sessionInvites.tokenHash, tokenHash)).get()
}

export function consumeSessionInvite(
  db: AppDb,
  input: { id: string; consumedAt: string; now: string },
): boolean {
  const result = db.update(sessionInvites)
    .set({ consumedAt: input.consumedAt })
    .where(and(
      eq(sessionInvites.id, input.id),
      isNull(sessionInvites.consumedAt),
      gt(sessionInvites.expiresAt, input.now),
    ))
    .run()

  return result.changes === 1
}
