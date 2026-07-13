import { and, eq, isNotNull, lte } from "drizzle-orm"

import type { AppDb } from "../persistence/db.js"
import { listInstances, updateInstanceRuntime } from "../persistence/repositories/instances.js"
import type { InstanceRow } from "../persistence/schema.js"
import { sessions } from "../persistence/schema.js"
import { createSessionService } from "./session-service.js"
import type { WorkerInstanceResponse } from "./worker-client.js"

export async function runReconcilePass(deps: {
  db: AppDb
  now: Date
  stopManagedInstance: (instanceId: string) => Promise<void>
  inspectRunningInstance: (instanceId: string) => Promise<WorkerInstanceResponse>
}): Promise<void> {
  const { db, now, stopManagedInstance, inspectRunningInstance } = deps
  const nowIso = now.toISOString()

  const expiredIdleSessions = db.select()
    .from(sessions)
    .where(and(
      eq(sessions.status, "idle"),
      isNotNull(sessions.idleExpiresAt),
      lte(sessions.idleExpiresAt, nowIso),
    ))
    .all()

  for (const session of expiredIdleSessions) {
    await stopManagedInstance(session.instanceId)
    const sessionService = createSessionService({
      db,
      idleTtlMs: 0,
      now: () => now,
    })
    await sessionService.closeSession(session.id)
  }

  const runningInstances = listInstances(db).filter((instance) => instance.desiredState === "running")
  for (const instance of runningInstances) {
    const inspection = await inspectRunningInstance(instance.id)
    updateInstanceRuntime(db, {
      id: instance.id,
      observedState: inspection.state as InstanceRow["observedState"],
      runtimeHandle: inspection.handle,
      lastError: inspection.lastError ?? null,
    })
  }
}
