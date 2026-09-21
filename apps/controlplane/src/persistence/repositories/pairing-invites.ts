import { eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { pairingInvites, type NewPairingInviteRow, type PairingInviteRow } from "../schema.js"

export function createPairingInvite(db: AppDb, row: NewPairingInviteRow): void {
  db.insert(pairingInvites).values(row).run()
}

export function getPairingInviteByTokenHash(db: AppDb, tokenHash: string): PairingInviteRow | undefined {
  return db.select().from(pairingInvites).where(eq(pairingInvites.tokenHash, tokenHash)).get()
}

export function consumePairingInvite(
  db: AppDb,
  input: { id: string; consumedAt: string; consumedByPrincipalId: string },
): void {
  db.update(pairingInvites)
    .set({
      consumedAt: input.consumedAt,
      consumedByPrincipalId: input.consumedByPrincipalId,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(pairingInvites.id, input.id))
    .run()
}
