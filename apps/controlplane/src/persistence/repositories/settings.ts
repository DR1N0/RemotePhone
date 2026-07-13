import { eq, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import { settings, type NewSettingRow, type SettingRow } from "../schema.js"

export function putSetting<T>(db: AppDb, row: NewSettingRow<T>): void {
  const existing = getSetting(db, row.key)

  if (existing) {
    db.update(settings)
      .set({
        value: row.value,
        updatedAt: sql`CURRENT_TIMESTAMP`,
      })
      .where(eq(settings.key, row.key))
      .run()
    return
  }

  db.insert(settings).values(row).run()
}

export function getSetting<T>(db: AppDb, key: string): SettingRow<T> | undefined {
  return db.select().from(settings).where(eq(settings.key, key)).get() as SettingRow<T> | undefined
}
