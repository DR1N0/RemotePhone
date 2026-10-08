import { z } from "zod"

export const runtimeClassSchema = z.enum([
  "android-studio-emulator",
  "cuttlefish",
  "waydroid",
])

export const workerKindSchema = z.enum(["local"])

export const hostOsSchema = z.enum(["macos", "linux", "windows"])

export const placementPolicySchema = z.discriminatedUnion("kind", [
  z.object({ kind: z.literal("local-only") }),
  z.object({ kind: z.literal("best-fit") }),
  z.object({ kind: z.literal("specific-worker"), workerId: z.string().min(1) }),
])

export const resourceProfileSchema = z.object({
  cpuMillicores: z.number().int().positive(),
  memoryMb: z.number().int().positive(),
  diskMb: z.number().int().positive(),
  accelerationMode: z.enum(["forbid", "prefer", "require", "auto"]),
  streamProfile: z.enum(["prototype", "balanced", "high"]),
})

export const launchConfigSchema = z.object({
  headless: z.boolean(),
  readOnly: z.boolean(),
  wipeData: z.boolean(),
})

export const sessionRoleSchema = z.enum(["owner", "participant", "observer"])

export const createInstanceRequestSchema = z.object({
  name: z.string().min(1),
  templateId: z.string().min(1),
  placement: placementPolicySchema,
  resourceProfile: resourceProfileSchema,
  launchConfig: launchConfigSchema,
}).strict()

export const createSessionRequestSchema = z.object({
  instanceId: z.string().min(1),
  ownerClientId: z.string().min(1),
  ownerAbsenceTimeoutMs: z.number().int().positive().optional(),
}).strict()

const usernameSchema = z.string().regex(/^[a-z0-9_]{3,32}$/)
const loginUsernameSchema = z.string().trim().toLowerCase().regex(/^[a-z0-9_]{3,32}$/)
const passwordSchema = z.string().min(12).max(1024)
const deviceLabelSchema = z.string().trim().min(1).max(128)

export const setupAdminRequestSchema = z.object({
  username: usernameSchema,
  password: passwordSchema,
  deviceLabel: deviceLabelSchema,
  bootstrapSecret: z.string().min(1).max(256),
}).strict()

export const loginRequestSchema = z.object({
  username: loginUsernameSchema,
  password: passwordSchema,
  deviceLabel: deviceLabelSchema,
}).strict()

export const registerRequestSchema = z.object({
  inviteToken: z.string().min(1),
  username: loginUsernameSchema,
  password: passwordSchema,
  deviceLabel: deviceLabelSchema,
}).strict()

export const enrollRequestSchema = z.object({
  username: loginUsernameSchema,
  password: passwordSchema,
}).strict()

export const registrationInviteRequestSchema = z.object({
  ttlMs: z.number().int().min(5 * 60_000).max(7 * 24 * 60 * 60_000).optional(),
}).strict()

export const setUserTypeRequestSchema = z.object({
  type: z.enum(["registered", "admin"]),
}).strict()

export const createSignalingSessionRequestSchema = z.object({
  sessionId: z.string().min(1),
  attachmentId: z.string().min(1),
}).strict()
