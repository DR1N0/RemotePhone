import { eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { instances, type InstanceRow, type NewInstanceRow } from "../schema.js"

export function createInstance(db: AppDb, row: NewInstanceRow): void {
  db.insert(instances).values(row).run()
}

export function listInstances(db: AppDb): InstanceRow[] {
  return db.select().from(instances).all()
}

export function listInstancesByOwner(db: AppDb, ownerId: string): InstanceRow[] {
  return db.select().from(instances).where(eq(instances.ownerId, ownerId)).all()
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
