import { and, eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { principals, type NewPrincipalRow, type PrincipalRow, type PrincipalStatus } from "../schema.js"

export function createPrincipal(db: AppDb, row: NewPrincipalRow): void {
  db.insert(principals).values(row).run()
}

export function getPrincipalById(db: AppDb, id: string): PrincipalRow | undefined {
  return db.select().from(principals).where(eq(principals.id, id)).get()
}

export function getServerOwnerPrincipal(db: AppDb): PrincipalRow | undefined {
  return db.select().from(principals).where(eq(principals.isServerOwner, true)).get()
}

export function setPrincipalStatus(
  db: AppDb,
  input: { id: string; status: PrincipalStatus },
): void {
  db.update(principals)
    .set({
      status: input.status,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(principals.id, input.id))
    .run()
}
