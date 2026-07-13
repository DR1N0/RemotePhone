import { eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { attachments, type AttachmentRow, type NewAttachmentRow } from "../schema.js"

export function createAttachment(db: AppDb, row: NewAttachmentRow): void {
  db.insert(attachments).values(row).run()
}

export function listAttachmentsForSession(db: AppDb, sessionId: string): AttachmentRow[] {
  return db.select().from(attachments).where(eq(attachments.sessionId, sessionId)).all()
}

export function getAttachmentById(db: AppDb, id: string): AttachmentRow | undefined {
  return db.select().from(attachments).where(eq(attachments.id, id)).get()
}

export function getAttachmentByReconnectToken(db: AppDb, reconnectToken: string): AttachmentRow | undefined {
  return db.select().from(attachments).where(eq(attachments.reconnectToken, reconnectToken)).get()
}

export function updateAttachmentRole(
  db: AppDb,
  input: Pick<NewAttachmentRow, "id" | "role">,
): void {
  db.update(attachments)
    .set({
      role: input.role,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(attachments.id, input.id))
    .run()
}

export function updateAttachmentStatus(
  db: AppDb,
  input: Pick<NewAttachmentRow, "id" | "status">,
): void {
  db.update(attachments)
    .set({
      status: input.status,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(attachments.id, input.id))
    .run()
}
