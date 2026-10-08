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

export function setAttachmentPendingTransportRole(
  db: AppDb,
  input: Pick<NewAttachmentRow, "id" | "pendingTargetRole">,
): void {
  db.update(attachments)
    .set({
      restrictivePendingRole: input.pendingTargetRole == null ? null : "observer",
      pendingTargetRole: input.pendingTargetRole,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(attachments.id, input.id))
    .run()
}

export function setAttachmentTransportTeardownPending(
  db: AppDb,
  input: { id: string; pending: boolean },
): void {
  db.update(attachments)
    .set({
      transportTeardownPending: input.pending,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(attachments.id, input.id))
    .run()
}

export function listPendingTransportTeardownAttachments(db: AppDb): AttachmentRow[] {
  return db.select().from(attachments).where(eq(attachments.transportTeardownPending, true)).all()
}

export function listAttachmentsForUser(db: AppDb, userId: string): AttachmentRow[] {
  return db.select().from(attachments).where(eq(attachments.userId, userId)).all()
}
