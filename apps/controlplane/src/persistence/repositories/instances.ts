import { eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { instances, type InstanceRow, type NewInstanceRow } from "../schema.js"

export function createInstance(db: AppDb, row: NewInstanceRow): void {
  db.insert(instances).values(row).run()
}

function isOwnerUniqueConstraint(error: unknown): boolean {
  return error instanceof Error
    && error.message.includes("UNIQUE constraint failed: instances.owner_id")
}

export function createInstanceIfOwnerAbsent(db: AppDb, row: NewInstanceRow): boolean {
  const transaction = db.$client.transaction(() => {
    if (listInstancesByOwner(db, row.ownerId).length > 0) {
      return false
    }
    createInstance(db, row)
    return true
  })

  try {
    return transaction.immediate()
  } catch (error) {
    if (isOwnerUniqueConstraint(error)) {
      return false
    }
    throw error
  }
}

export function listInstances(db: AppDb): InstanceRow[] {
  return db.select().from(instances).all()
}

export function listInstancesByOwner(db: AppDb, ownerId: string): InstanceRow[] {
  return db.select().from(instances).where(eq(instances.ownerId, ownerId)).all()
}

export type OwnerConflict = {
  ownerId: string
  instanceIds: string[]
  reason: "duplicate" | "invalid-owner"
}

/**
 * Reports legacy owner data that would make a physical unique index unsafe.
 * It deliberately does not repair or delete rows so an administrator can decide.
 */
export function inspectOwnerConflicts(db: AppDb): OwnerConflict[] {
  const duplicateRows = db.$client.prepare(`
    SELECT owner_id AS ownerId, group_concat(id, ',') AS instanceIds
    FROM (SELECT id, owner_id FROM instances ORDER BY id)
    GROUP BY owner_id
    HAVING count(*) > 1
  `).all() as Array<{ ownerId: string; instanceIds: string }>
  const invalidRows = db.$client.prepare(`
    SELECT legacy_instances.owner_id AS ownerId, group_concat(legacy_instances.id, ',') AS instanceIds
    FROM (SELECT id, owner_id FROM instances ORDER BY id) AS legacy_instances
    LEFT JOIN users ON users.id = legacy_instances.owner_id
    WHERE users.id IS NULL
    GROUP BY legacy_instances.owner_id
  `).all() as Array<{ ownerId: string; instanceIds: string }>

  return [
    ...duplicateRows.map(({ ownerId, instanceIds }) => ({ ownerId, instanceIds: instanceIds.split(","), reason: "duplicate" as const })),
    ...invalidRows.map(({ ownerId, instanceIds }) => ({ ownerId, instanceIds: instanceIds.split(","), reason: "invalid-owner" as const })),
  ].sort((left, right) => left.ownerId.localeCompare(right.ownerId) || left.reason.localeCompare(right.reason))
}

/** Returns false without changing data when legacy conflicts need remediation. */
export function ensureUniqueInstanceOwnerIndex(db: AppDb): boolean {
  if (inspectOwnerConflicts(db).length > 0) {
    return false
  }
  db.$client.exec("CREATE UNIQUE INDEX IF NOT EXISTS instances_unique_owner ON instances(owner_id)")
  return true
}

export function getInstanceById(db: AppDb, id: string): InstanceRow | undefined {
  return db.select().from(instances).where(eq(instances.id, id)).get()
}

export function updateInstanceRuntime(
  db: AppDb,
  input: Pick<NewInstanceRow, "id" | "observedState" | "runtimeHandle" | "lastError">,
): void {
  db.update(instances)
    .set({
      observedState: input.observedState,
      runtimeHandle: input.runtimeHandle,
      lastError: input.lastError,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(instances.id, input.id))
    .run()
}

export function updateInstanceLifecycle(
  db: AppDb,
  input: Pick<NewInstanceRow, "id" | "desiredState" | "observedState" | "runtimeHandle" | "lastError">,
): void {
  db.update(instances)
    .set({
      desiredState: input.desiredState,
      observedState: input.observedState,
      runtimeHandle: input.runtimeHandle,
      lastError: input.lastError,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(instances.id, input.id))
    .run()
}

export function deleteInstanceById(db: AppDb, id: string): void {
  db.delete(instances).where(eq(instances.id, id)).run()
}
