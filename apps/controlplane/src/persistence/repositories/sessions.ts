import { and, eq, isNotNull, ne } from "drizzle-orm"
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

export function listIdleSessions(db: AppDb): SessionRow[] {
  return db.select().from(sessions)
    .where(and(eq(sessions.status, "idle"), isNotNull(sessions.idleExpiresAt)))
    .all()
}

export function markSessionIdle(
  db: AppDb,
  input: Pick<NewSessionRow, "id" | "idleExpiresAt">,
): void {
  db.update(sessions)
    .set({
      status: "idle",
      idleExpiresAt: input.idleExpiresAt,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(sessions.id, input.id))
    .run()
}

export function activateSession(db: AppDb, input: Pick<NewSessionRow, "id">): void {
  db.update(sessions)
    .set({
      status: "active",
      idleExpiresAt: null,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(sessions.id, input.id))
    .run()
}

export function closeSession(db: AppDb, input: Pick<NewSessionRow, "id">): void {
  db.update(sessions)
    .set({
      status: "closed",
      idleExpiresAt: null,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(sessions.id, input.id))
    .run()
}
