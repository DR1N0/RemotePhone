import { describe, expect, it } from "vitest"
import {
  createInstanceRequestSchema,
  createSessionRequestSchema,
  hostOsSchema,
  joinSessionRequestSchema,
  launchConfigSchema,
  placementPolicySchema,
  resourceProfileSchema,
  runtimeClassSchema,
  sessionRoleSchema,
  workerKindSchema,
} from "../src/domain/schemas.js"

describe("domain schemas", () => {
  it("accepts the supported runtime classes, worker kinds, and host operating systems", () => {
    expect(runtimeClassSchema.parse("android-studio-emulator")).toBe("android-studio-emulator")
    expect(runtimeClassSchema.parse("cuttlefish")).toBe("cuttlefish")
    expect(runtimeClassSchema.parse("waydroid")).toBe("waydroid")

    expect(workerKindSchema.parse("local")).toBe("local")

    expect(hostOsSchema.parse("macos")).toBe("macos")
    expect(hostOsSchema.parse("linux")).toBe("linux")
    expect(hostOsSchema.parse("windows")).toBe("windows")
  })

  it("accepts the supported placement policy variants", () => {
    expect(placementPolicySchema.parse({ kind: "local-only" })).toEqual({ kind: "local-only" })
    expect(placementPolicySchema.parse({ kind: "best-fit" })).toEqual({ kind: "best-fit" })
    expect(placementPolicySchema.parse({ kind: "specific-worker", workerId: "worker-1" })).toEqual({
      kind: "specific-worker",
      workerId: "worker-1",
    })
  })

  it("rejects specific-worker placement without a worker id", () => {
    const parsed = placementPolicySchema.safeParse({ kind: "specific-worker" })

    expect(parsed.success).toBe(false)
  })

  it("accepts the supported resource profile contract", () => {
    expect(
      resourceProfileSchema.parse({
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      }),
    ).toEqual({
      cpuMillicores: 1000,
      memoryMb: 4096,
      diskMb: 8192,
      accelerationMode: "auto",
      streamProfile: "prototype",
    })
  })

  it("accepts the initial launch configuration contract", () => {
    expect(
      launchConfigSchema.parse({
        headless: true,
        readOnly: true,
        wipeData: false,
      }),
    ).toEqual({
      headless: true,
      readOnly: true,
      wipeData: false,
    })
  })

  it("enumerates supported session roles", () => {
    expect(sessionRoleSchema.parse("owner")).toBe("owner")
    expect(sessionRoleSchema.parse("participant")).toBe("participant")
    expect(sessionRoleSchema.parse("observer")).toBe("observer")
  })

  it("accepts a local-only whole-phone create-instance request", () => {
    const parsed = createInstanceRequestSchema.parse({
      name: "phone-1",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      },
      launchConfig: {
        headless: true,
        readOnly: true,
        wipeData: false,
      },
    })

    expect(parsed.templateId).toBe("macos-android-studio:Medium_Phone")
    expect(parsed.placement.kind).toBe("local-only")
  })

  it("rejects create-instance requests that include an explicit runtime class", () => {
    const parsed = createInstanceRequestSchema.safeParse({
      name: "phone-1",
      runtimeClass: "android-studio-emulator",
      templateId: "macos-android-studio:Medium_Phone",
      placement: { kind: "local-only" },
      resourceProfile: {
        cpuMillicores: 1000,
        memoryMb: 4096,
        diskMb: 8192,
        accelerationMode: "auto",
        streamProfile: "prototype",
      },
      launchConfig: {
        headless: true,
        readOnly: true,
        wipeData: false,
      },
    })

    expect(parsed.success).toBe(false)
  })

  it("accepts a create-session request with an instance id and owner client id", () => {
    expect(createSessionRequestSchema.parse({
      instanceId: "inst_1",
      ownerClientId: "owner_1",
    })).toEqual({
      instanceId: "inst_1",
      ownerClientId: "owner_1",
    })
  })

  it("accepts a join-session request with a client id only", () => {
    expect(joinSessionRequestSchema.parse({
      clientId: "observer_1",
    })).toEqual({
      clientId: "observer_1",
    })
  })
})
