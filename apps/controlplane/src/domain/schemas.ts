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
}).strict()

export const joinSessionRequestSchema = z.object({
  clientId: z.string().min(1),
}).strict()

export const resumeSessionRequestSchema = z.object({
  reconnectToken: z.string().min(1),
}).strict()

export const createSignalingSessionRequestSchema = z.object({
  sessionId: z.string().min(1),
  attachmentId: z.string().min(1),
}).strict()
