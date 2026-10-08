import { sql } from "drizzle-orm"
import { integer, sqliteTable, text } from "drizzle-orm/sqlite-core"

import type { InstanceDesiredState, InstanceObservedState, LaunchConfig, PlacementPolicy, ResourceProfile, RuntimeClass, RuntimeHandle, SessionRole } from "../domain/types.js"

export type UserStatus = "active" | "banned"
export type UserType = "registered" | "admin" | "guest"
export type CredentialScope = "account" | "attachment"
export type SessionStatus = "active" | "closed"
export type AttachmentStatus = "connected" | "disconnected"
export type RestrictivePendingRole = "observer"
export type PendingTargetRole = "participant" | "observer"

function jsonTextColumn<T>(name: string) { return text(name, { mode: "json" }).$type<T>() }
const timestampColumns = {
  createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
  updatedAt: text("updated_at").notNull().default(sql`CURRENT_TIMESTAMP`),
}

export const users = sqliteTable("users", {
  id: text("id").primaryKey(),
  displayName: text("display_name").notNull(),
  userType: text("user_type").$type<UserType>().notNull().default("registered"),
  username: text("username"),
  passwordHash: text("password_hash"),
  enrollmentRequired: integer("enrollment_required", { mode: "boolean" }).notNull().default(true),
  isServerOwner: integer("is_server_owner", { mode: "boolean" }).notNull().default(false),
  status: text("status").$type<UserStatus>().notNull().default("active"),
  ...timestampColumns,
})

export const instances = sqliteTable("instances", {
  id: text("id").primaryKey(),
  ownerId: text("owner_id").notNull().references(() => users.id),
  name: text("name").notNull(), runtimeClass: text("runtime_class").$type<RuntimeClass>().notNull(), templateId: text("template_id").notNull(),
  placementPolicy: jsonTextColumn<PlacementPolicy>("placement_policy_json").notNull(), resourceProfile: jsonTextColumn<ResourceProfile>("resource_profile_json").notNull(), launchConfig: jsonTextColumn<LaunchConfig>("launch_config_json").notNull(),
  desiredState: text("desired_state").$type<InstanceDesiredState>().notNull(), observedState: text("observed_state").$type<InstanceObservedState>().notNull(), runtimeHandle: jsonTextColumn<RuntimeHandle>("runtime_handle_json"), stateDataRef: text("state_data_ref"), lastError: text("last_error"), ...timestampColumns,
})

export const sessions = sqliteTable("sessions", {
  id: text("id").primaryKey(), instanceId: text("instance_id").notNull().references(() => instances.id), status: text("status").$type<SessionStatus>().notNull(), ownerAbsenceDeadline: text("owner_absence_deadline"), ownerAbsenceTimeoutMs: integer("owner_absence_timeout_ms"), reconnectToken: text("reconnect_token"), ...timestampColumns,
})

export const attachments = sqliteTable("attachments", {
  id: text("id").primaryKey(), sessionId: text("session_id").notNull().references(() => sessions.id), userId: text("user_id").references(() => users.id), clientId: text("client_id").notNull(), role: text("role").$type<SessionRole>().notNull(), status: text("status").$type<AttachmentStatus>().notNull(), reconnectToken: text("reconnect_token"), transportTeardownPending: integer("transport_teardown_pending", { mode: "boolean" }).notNull().default(false), restrictivePendingRole: text("restrictive_pending_role").$type<RestrictivePendingRole>(), pendingTargetRole: text("pending_target_role").$type<PendingTargetRole>(), ...timestampColumns,
})

export const credentials = sqliteTable("credentials", {
  id: text("id").primaryKey(), userId: text("user_id").notNull().references(() => users.id), tokenHash: text("token_hash").notNull(), deviceLabel: text("device_label").notNull(), scope: text("scope").$type<CredentialScope>().notNull().default("account"), sessionId: text("session_id").references(() => sessions.id), attachmentId: text("attachment_id").references(() => attachments.id), notBefore: text("not_before"), expiresAt: text("expires_at"), revokedAt: text("revoked_at"), ...timestampColumns,
})

export const pairingInvites = sqliteTable("pairing_invites", {
  id: text("id").primaryKey(), tokenHash: text("token_hash").notNull(), createdBy: text("created_by").notNull().references(() => users.id), expiresAt: text("expires_at").notNull(), consumedAt: text("consumed_at"), consumedByUserId: text("consumed_by_user_id").references(() => users.id), ...timestampColumns,
})

export const sessionInvites = sqliteTable("session_invites", {
  id: text("id").primaryKey(), sessionId: text("session_id").notNull().references(() => sessions.id), createdByUserId: text("created_by_user_id").notNull().references(() => users.id), tokenHash: text("token_hash").notNull().unique(), expiresAt: text("expires_at").notNull(), consumedAt: text("consumed_at"), createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
})

export const auditEvents = sqliteTable("audit_events", {
  id: text("id").primaryKey(), actorUserId: text("actor_user_id").references(() => users.id), actorCredentialId: text("actor_credential_id").references(() => credentials.id), action: text("action").notNull(), targetType: text("target_type").notNull(), targetId: text("target_id").notNull(), requestId: text("request_id").notNull(), outcome: text("outcome").notNull(), createdAt: text("created_at").notNull().default(sql`CURRENT_TIMESTAMP`),
})

export const settings = sqliteTable("settings", { key: text("key").primaryKey(), value: jsonTextColumn<unknown>("value_json").notNull(), updatedAt: text("updated_at").notNull().default(sql`CURRENT_TIMESTAMP`) })
export const appSchema = { attachments, auditEvents, credentials, instances, pairingInvites, sessionInvites, sessions, settings, users }
export type InstanceRow = typeof instances.$inferSelect
export type NewInstanceRow = typeof instances.$inferInsert
export type SessionRow = typeof sessions.$inferSelect
export type NewSessionRow = typeof sessions.$inferInsert
export type AttachmentRow = typeof attachments.$inferSelect
export type NewAttachmentRow = typeof attachments.$inferInsert
export type UserRow = typeof users.$inferSelect
export type NewUserRow = typeof users.$inferInsert
export type UserDto = Pick<UserRow, "id" | "username" | "displayName" | "status"> & { type: UserType }
export type CredentialRow = typeof credentials.$inferSelect
export type NewCredentialRow = typeof credentials.$inferInsert
export type PairingInviteRow = typeof pairingInvites.$inferSelect
export type NewPairingInviteRow = typeof pairingInvites.$inferInsert
export type SessionInviteRow = typeof sessionInvites.$inferSelect
export type NewSessionInviteRow = typeof sessionInvites.$inferInsert
export type AuditEventRow = typeof auditEvents.$inferSelect
export type NewAuditEventRow = typeof auditEvents.$inferInsert
type BaseSettingRow = typeof settings.$inferSelect
type BaseNewSettingRow = typeof settings.$inferInsert
export type SettingRow<T = unknown> = Omit<BaseSettingRow, "value"> & { value: T }
export type NewSettingRow<T = unknown> = Omit<BaseNewSettingRow, "value"> & { value: T }
