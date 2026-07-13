export type RuntimeClass = "android-studio-emulator" | "cuttlefish" | "waydroid"

export type WorkerKind = "local"

export type HostOS = "macos" | "linux" | "windows"

export type PlacementPolicy =
  | { kind: "local-only" }
  | { kind: "best-fit" }
  | { kind: "specific-worker"; workerId: string }

export type ResourceProfile = {
  cpuMillicores: number
  memoryMb: number
  diskMb: number
  accelerationMode: "forbid" | "prefer" | "require" | "auto"
  streamProfile: "prototype" | "balanced" | "high"
}

export type LaunchConfig = {
  headless: boolean
  readOnly: boolean
  wipeData: boolean
}

export type InstanceDesiredState = "stopped" | "running"

export type InstanceObservedState =
  | "stopped"
  | "starting"
  | "running"
  | "idle"
  | "stopping"
  | "failed"
  | "degraded"
  | "unknown"

export type RuntimeHandle = {
  workerId: string
  pid: number
  instanceId?: string
  templateId?: string
  runtimeClass?: RuntimeClass
  consolePort?: number
  adbPort?: number
  grpcPort?: number
}

export type SessionRole = "owner" | "participant" | "observer"

export type CreateInstanceRequest = {
  name: string
  templateId: string
  placement: PlacementPolicy
  resourceProfile: ResourceProfile
  launchConfig: LaunchConfig
}
