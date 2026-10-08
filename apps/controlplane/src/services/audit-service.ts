import { randomUUID } from "node:crypto"

import type { AppDb } from "../persistence/db.js"
import { createAuditEvent, listAuditEvents } from "../persistence/repositories/audit-events.js"
import type { AuditEventRow } from "../persistence/schema.js"

export type AuditOutcome = "attempt" | "success" | "failure"
export type AuditAction =
  | "user.promote"
  | "user.demote"
  | "user.ban"
  | "user.unban"
  | "instance.start"
  | "instance.stop"
  | "instance.delete"
  | "registration.invite.issue"
  | "registration.invite.redeem"
  | "session.invite.issue"
  | "session.invite.redeem"
  | "session.role.change"
  | "session.attachment.disconnect"
  | "session.close"
  | "admin.guest.revoke"
  | "user.credential.revoke"
  | "transport.teardown"
  | "transport.create"
  | "transport.role.update"

export type AuditContext = {
  actorUserId: string | null
  actorCredentialId: string | null
  requestId: string
}

export type AuditRecord = AuditContext & {
  action: AuditAction
  targetType: string
  targetId: string
  outcome: AuditOutcome
}

export type AuditDto = Pick<AuditEventRow,
  "id" | "actorUserId" | "actorCredentialId" | "action" | "targetType" | "targetId" | "requestId" | "outcome" | "createdAt"
>

export type AuditService = ReturnType<typeof createAuditService>

function toAuditDto(row: AuditEventRow): AuditDto {
  return {
    id: row.id,
    actorUserId: row.actorUserId,
    actorCredentialId: row.actorCredentialId,
    action: row.action,
    targetType: row.targetType,
    targetId: row.targetId,
    requestId: row.requestId,
    outcome: row.outcome,
    createdAt: row.createdAt,
  }
}

export function createAuditContext(
  actorUserId: string | null,
  actorCredentialId: string | null,
  requestId: string = randomUUID(),
): AuditContext {
  return { actorUserId, actorCredentialId, requestId }
}

export function createAuditService(deps: { db: AppDb }) {
  return {
    record(input: AuditRecord): void {
      createAuditEvent(deps.db, {
        id: randomUUID(),
        actorUserId: input.actorUserId,
        actorCredentialId: input.actorCredentialId,
        action: input.action,
        targetType: input.targetType,
        targetId: input.targetId,
        requestId: input.requestId,
        outcome: input.outcome,
      })
    },

    list(input: { limit: number; cursor: string | null }): { items: AuditDto[]; nextCursor: string | null } {
      const page = listAuditEvents(deps.db, input)
      return {
        items: page.items.map(toAuditDto),
        nextCursor: page.nextCursor,
      }
    },
  }
}
