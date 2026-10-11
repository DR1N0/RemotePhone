// Package contracts holds the wire-contract types shared between the
// control plane and the worker: the worker protocol's request/response
// shapes and the domain enums both sides agree on. This is the only
// package both binaries are expected to import.
package contracts

type RuntimeClass string

const (
	RuntimeAndroidStudioEmulator RuntimeClass = "android-studio-emulator"
	RuntimeCuttlefish            RuntimeClass = "cuttlefish"
	RuntimeWaydroid              RuntimeClass = "waydroid"
)

type WorkerKind string

const WorkerKindLocal WorkerKind = "local"

type HostOS string

const (
	HostMacOS   HostOS = "macos"
	HostLinux   HostOS = "linux"
	HostWindows HostOS = "windows"
)

type PlacementKind string

const (
	PlacementLocalOnly      PlacementKind = "local-only"
	PlacementBestFit        PlacementKind = "best-fit"
	PlacementSpecificWorker PlacementKind = "specific-worker"
)

type PlacementPolicy struct {
	Kind     PlacementKind `json:"kind"`
	WorkerID string        `json:"workerId,omitempty"`
}

type AccelerationMode string

const (
	AccelerationForbid  AccelerationMode = "forbid"
	AccelerationPrefer  AccelerationMode = "prefer"
	AccelerationRequire AccelerationMode = "require"
	AccelerationAuto    AccelerationMode = "auto"
)

type StreamProfile string

const (
	StreamPrototype StreamProfile = "prototype"
	StreamBalanced  StreamProfile = "balanced"
	StreamHigh      StreamProfile = "high"
)

type ResourceProfile struct {
	CPUMillicores    int              `json:"cpuMillicores"`
	MemoryMB         int              `json:"memoryMb"`
	DiskMB           int              `json:"diskMb"`
	AccelerationMode AccelerationMode `json:"accelerationMode"`
	StreamProfile    StreamProfile    `json:"streamProfile"`
}

type LaunchConfig struct {
	Headless bool `json:"headless"`
	ReadOnly bool `json:"readOnly"`
	WipeData bool `json:"wipeData"`
}

type InstanceDesiredState string

const (
	DesiredStopped InstanceDesiredState = "stopped"
	DesiredRunning InstanceDesiredState = "running"
)

type InstanceObservedState string

const (
	ObservedStopped  InstanceObservedState = "stopped"
	ObservedStarting InstanceObservedState = "starting"
	ObservedRunning  InstanceObservedState = "running"
	ObservedIdle     InstanceObservedState = "idle"
	ObservedStopping InstanceObservedState = "stopping"
	ObservedFailed   InstanceObservedState = "failed"
	ObservedDegraded InstanceObservedState = "degraded"
	ObservedUnknown  InstanceObservedState = "unknown"
)

type RuntimeHandle struct {
	WorkerID     string       `json:"workerId"`
	PID          int          `json:"pid"`
	InstanceID   string       `json:"instanceId,omitempty"`
	TemplateID   string       `json:"templateId,omitempty"`
	RuntimeClass RuntimeClass `json:"runtimeClass,omitempty"`
	ConsolePort  int          `json:"consolePort,omitempty"`
	ADBPort      int          `json:"adbPort,omitempty"`
	GRPCPort     int          `json:"grpcPort,omitempty"`
}

type SessionRole string

const (
	RoleOwner       SessionRole = "owner"
	RoleParticipant SessionRole = "participant"
	RoleObserver    SessionRole = "observer"
)
