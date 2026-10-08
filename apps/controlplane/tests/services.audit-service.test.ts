import { beforeEach, describe, expect, it } from "vitest"

import { createTestDb, resetTestDb } from "../src/persistence/db.js"
import { listAuditEvents } from "../src/persistence/repositories/audit-events.js"
import { createAuditService } from "../src/services/audit-service.js"

describe("audit service", () => {
  beforeEach(() => resetTestDb())

  it("writes an immutable event with all correlation fields", () => {
    const db = createTestDb()
    const auditService = createAuditService({ db })

    auditService.record({
      actorUserId: null,
      actorCredentialId: null,
      action: "session.invite.redeem",
      targetType: "session_invite",
      targetId: "invite-1",
      requestId: "request-1",
      outcome: "success",
    })

    expect(listAuditEvents(db, { limit: 20, cursor: null }).items).toContainEqual(expect.objectContaining({
      actorUserId: null,
      actorCredentialId: null,
      action: "session.invite.redeem",
      targetType: "session_invite",
      targetId: "invite-1",
      requestId: "request-1",
      outcome: "success",
      createdAt: expect.any(String),
    }))
  })
})
