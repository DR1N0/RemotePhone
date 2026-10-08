import { and, eq, isNotNull, lte, ne } from "drizzle-orm"
import { sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { sessions, type NewSessionRow, type SessionRow } from "../schema.js"

export function createSession(db: AppDb, row: NewSessionRow): void {
  db.insert(sessions).values(row).run()
}

export function listSessionsForInstance(db: AppDb, instanceId: string): SessionRow[] {
  return db.select().from(sessions).where(eq(sessions.instanceId, instanceId)).all()
}

export function getSessionById(db: AppDb, id: string): SessionRow | undefined {
  return db.select().from(sessions).where(eq(sessions.id, id)).get()
}

export function findOpenSessionForInstance(db: AppDb, instanceId: string): SessionRow | undefined {
  return db.select().from(sessions)
    .where(and(eq(sessions.instanceId, instanceId), ne(sessions.status, "closed")))
    .get()
}

export function listSessionsWithExpiredOwnerAbsence(db: AppDb, nowIso: string): SessionRow[] {
  return db.select().from(sessions)
    .where(and(
      eq(sessions.status, "active"),
      isNotNull(sessions.ownerAbsenceDeadline),
      lte(sessions.ownerAbsenceDeadline, nowIso),
    ))
    .all()
}

export function setOwnerAbsenceDeadline(
  db: AppDb,
  input: { id: string; ownerAbsenceDeadline: string },
): void {
  db.update(sessions)
    .set({
      ownerAbsenceDeadline: input.ownerAbsenceDeadline,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(sessions.id, input.id))
    .run()
}

export function clearOwnerAbsenceDeadline(db: AppDb, input: { id: string }): void {
  db.update(sessions)
    .set({
      ownerAbsenceDeadline: null,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(sessions.id, input.id))
    .run()
}

export function closeSession(db: AppDb, input: Pick<NewSessionRow, "id">): void {
  db.update(sessions)
    .set({
      status: "closed",
      ownerAbsenceDeadline: null,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(sessions.id, input.id))
    .run()
}
