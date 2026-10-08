import { and, desc, eq, lt, or } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { auditEvents, type AuditEventRow, type NewAuditEventRow } from "../schema.js"

export class InvalidAuditCursorError extends Error {}

export function createAuditEvent(db: AppDb, row: NewAuditEventRow): void {
  db.insert(auditEvents).values(row).run()
}

export function listAuditEvents(
  db: AppDb,
  input: { limit: number; cursor: string | null },
): { items: AuditEventRow[]; nextCursor: string | null } {
  if (!Number.isInteger(input.limit) || input.limit < 1 || input.limit > 100) {
    throw new RangeError("audit limit must be an integer from 1 to 100")
  }

  const cursor = input.cursor == null
    ? null
    : db.select({ id: auditEvents.id, createdAt: auditEvents.createdAt })
      .from(auditEvents)
      .where(eq(auditEvents.id, input.cursor))
      .get()
  if (input.cursor != null && cursor == null) {
    throw new InvalidAuditCursorError(`invalid audit cursor: ${input.cursor}`)
  }

  const page = db.select().from(auditEvents)
    .where(cursor == null
      ? undefined
      : or(
        lt(auditEvents.createdAt, cursor.createdAt),
        and(eq(auditEvents.createdAt, cursor.createdAt), lt(auditEvents.id, cursor.id)),
      ))
    .orderBy(desc(auditEvents.createdAt), desc(auditEvents.id))
    .limit(input.limit + 1)
    .all()
  const items = page.slice(0, input.limit)
  return {
    items,
    nextCursor: page.length > input.limit ? items.at(-1)?.id ?? null : null,
  }
}
