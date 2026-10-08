import { and, asc, count, eq, gt, ne, sql } from "drizzle-orm"

import type { AppDb } from "../db.js"
import {
  users,
  type NewUserRow,
  type UserDto,
  type UserRow,
  type UserStatus,
  type UserType,
} from "../schema.js"

export function normalizeUsername(username: string): string {
  return username.trim().toLowerCase()
}

export function createUser(db: AppDb, row: NewUserRow): void {
  if (row.userType === "guest" && row.username != null) {
    throw new Error("guest users cannot have a login username")
  }

  db.insert(users).values({
    ...row,
    username: row.username == null ? null : normalizeUsername(row.username),
  }).run()
}

export function getUserById(db: AppDb, id: string): UserRow | undefined {
  return db.select().from(users).where(eq(users.id, id)).get()
}

export function getUserByUsername(db: AppDb, username: string): UserRow | undefined {
  const normalizedUsername = normalizeUsername(username)
  return db.select().from(users)
    .where(sql`lower(trim(${users.username})) = ${normalizedUsername}`)
    .get()
}

export function getServerOwnerUser(db: AppDb): UserRow | undefined {
  return db.select().from(users).where(eq(users.isServerOwner, true)).get()
}

export function listUsers(db: AppDb): UserRow[] {
  return db.select().from(users).all()
}

export function listAccountUsers(
  db: AppDb,
  input: { limit: number; cursor: string | null },
): { items: UserRow[]; nextCursor: string | null } {
  const page = db.select().from(users)
    .where(input.cursor == null
      ? ne(users.userType, "guest")
      : and(ne(users.userType, "guest"), gt(users.id, input.cursor)))
    .orderBy(asc(users.id))
    .limit(input.limit + 1)
    .all()
  const items = page.slice(0, input.limit)
  return {
    items,
    nextCursor: page.length > input.limit ? items.at(-1)?.id ?? null : null,
  }
}

export function countAdmins(db: AppDb): number {
  return db.select({ count: count() }).from(users)
    .where(eq(users.userType, "admin"))
    .get()?.count ?? 0
}

export function countRegisteredOrAdminUsers(db: AppDb): number {
  return db.select({ count: count() }).from(users)
    .where(ne(users.userType, "guest"))
    .get()?.count ?? 0
}

export function countActiveAdmins(db: AppDb): number {
  return db.select({ count: count() }).from(users)
    .where(and(eq(users.userType, "admin"), eq(users.status, "active")))
    .get()?.count ?? 0
}

export function updateUserType(db: AppDb, input: { id: string; userType: Exclude<UserType, "guest"> }): void {
  db.update(users)
    .set({
      userType: input.userType,
      isServerOwner: input.userType === "admin",
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(users.id, input.id))
    .run()
}

export function setUserStatus(db: AppDb, input: { id: string; status: UserStatus }): void {
  db.update(users)
    .set({ status: input.status, updatedAt: sql`CURRENT_TIMESTAMP` })
    .where(eq(users.id, input.id))
    .run()
}

export function enrollUser(db: AppDb, input: { id: string; username: string; passwordHash: string }): void {
  db.update(users)
    .set({
      username: normalizeUsername(input.username),
      passwordHash: input.passwordHash,
      enrollmentRequired: false,
      updatedAt: sql`CURRENT_TIMESTAMP`,
    })
    .where(eq(users.id, input.id))
    .run()
}

export function toUserDto(row: UserRow): UserDto {
  return {
    id: row.id,
    type: row.userType,
    username: row.username,
    displayName: row.displayName,
    status: row.status,
  }
}
