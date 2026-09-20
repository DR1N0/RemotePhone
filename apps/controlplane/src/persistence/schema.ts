import { sql } from "drizzle-orm"
import { sqliteTable, text } from "drizzle-orm/sqlite-core"

import type {
  InstanceDesiredState,
  InstanceObservedState,
  LaunchConfig,
  PlacementPolicy,
  ResourceProfile,
  RuntimeClass,
  RuntimeHandle,
  SessionRole,
} from "../domain/types.js"

export type SessionStatus = "active" | "idle" | "closed"
export type AttachmentStatus = "connected" | "disconnected"
export type RestrictivePendingRole = "observer"
export type PendingTargetRole = "participant" | "observer"

function jsonTextColumn<T>(name: string) {
  return text(name, { mode: "json" }).$type<T>()
}

const timestampColumns = {
  createdAt: text("created_at")
    .notNull()
    .default(sql`CURRENT_TIMESTAMP`),
  updatedAt: text("updated_at")
    .notNull()
    .default(sql`CURRENT_TIMESTAMP`),
}

export const instances = sqliteTable("instances", {
  id: text("id").primaryKey(),
  name: text("name").notNull(),
  runtimeClass: text("runtime_class").$type<RuntimeClass>().notNull(),
  templateId: text("template_id").notNull(),
  placementPolicy: jsonTextColumn<PlacementPolicy>("placement_policy_json").notNull(),
  resourceProfile: jsonTextColumn<ResourceProfile>("resource_profile_json").notNull(),
  launchConfig: jsonTextColumn<LaunchConfig>("launch_config_json").notNull(),
  desiredState: text("desired_state").$type<InstanceDesiredState>().notNull(),
  observedState: text("observed_state").$type<InstanceObservedState>().notNull(),
  runtimeHandle: jsonTextColumn<RuntimeHandle>("runtime_handle_json"),
  stateDataRef: text("state_data_ref"),
  lastError: text("last_error"),
  ...timestampColumns,
})

export const sessions = sqliteTable("sessions", {
  id: text("id").primaryKey(),
  instanceId: text("instance_id")
    .notNull()
    .references(() => instances.id),
  status: text("status").$type<SessionStatus>().notNull(),
  idleExpiresAt: text("idle_expires_at"),
  reconnectToken: text("reconnect_token"),
  ...timestampColumns,
})

export const attachments = sqliteTable("attachments", {
  id: text("id").primaryKey(),
  sessionId: text("session_id")
    .notNull()
    .references(() => sessions.id),
  clientId: text("client_id").notNull(),
  role: text("role").$type<SessionRole>().notNull(),
  status: text("status").$type<AttachmentStatus>().notNull(),
  reconnectToken: text("reconnect_token"),
  inviteToken: text("invite_token"),
  restrictivePendingRole: text("restrictive_pending_role").$type<RestrictivePendingRole>(),
  pendingTargetRole: text("pending_target_role").$type<PendingTargetRole>(),
  ...timestampColumns,
})

export const settings = sqliteTable("settings", {
  key: text("key").primaryKey(),
  value: jsonTextColumn<unknown>("value_json").notNull(),
  updatedAt: text("updated_at")
    .notNull()
    .default(sql`CURRENT_TIMESTAMP`),
})

export const appSchema = {
  attachments,
  instances,
  sessions,
  settings,
}

export type InstanceRow = typeof instances.$inferSelect
export type NewInstanceRow = typeof instances.$inferInsert

export type SessionRow = typeof sessions.$inferSelect
export type NewSessionRow = typeof sessions.$inferInsert

export type AttachmentRow = typeof attachments.$inferSelect
export type NewAttachmentRow = typeof attachments.$inferInsert

type BaseSettingRow = typeof settings.$inferSelect
type BaseNewSettingRow = typeof settings.$inferInsert

export type SettingRow<T = unknown> = Omit<BaseSettingRow, "value"> & { value: T }
export type NewSettingRow<T = unknown> = Omit<BaseNewSettingRow, "value"> & { value: T }
