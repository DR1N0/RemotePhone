package runtime

import "errors"

var (
	ErrAdapterNotFound  = errors.New("runtime adapter not found")
	ErrInstanceNotFound = errors.New("runtime instance not found")
	ErrTemplateNotFound = errors.New("runtime template not found")
)

type Template struct {
	TemplateID   string            `json:"templateId"`
	RuntimeClass string            `json:"runtimeClass"`
	DisplayName  string            `json:"displayName"`
	Metadata     map[string]string `json:"metadata,omitempty"`
}

type LaunchConfig struct {
	Headless bool `json:"headless"`
	ReadOnly bool `json:"readOnly"`
	WipeData bool `json:"wipeData"`
}

type StartRequest struct {
	InstanceID   string       `json:"instanceId"`
	RuntimeClass string       `json:"runtimeClass"`
	TemplateID   string       `json:"templateId"`
	LaunchConfig LaunchConfig `json:"launchConfig"`
	// BaseImageArtifactPath, if set, is where a promoted base image for
	// this template was captured to (config.ini + a seed userdata image —
	// see androidstudio.ensureInstanceAVD) — the control plane resolves
	// this once per start by looking up the template's current base image,
	// empty if none has ever been promoted. When set, a new instance's AVD
	// is seeded from here instead of the template's live, on-disk AVD.
	BaseImageArtifactPath string `json:"baseImageArtifactPath,omitempty"`
}

// InstanceHandle is what's persisted in instances.runtime_handle_json.
// PID/ConsolePort/AdbPort/GRPCPort describe the emulator process this
// worker launched; WorkerPID/WorkerBaseURL describe the mako-worker OS
// process itself (control-plane-spawned, one per running instance — see
// docs/plans/worker-lifecycle.md). The two are independent: a worker
// process can crash and be respawned (new WorkerPID/WorkerBaseURL) while
// the emulator process it adopts keeps running unchanged.
type InstanceHandle struct {
	InstanceID    string `json:"instanceId"`
	TemplateID    string `json:"templateId"`
	RuntimeClass  string `json:"runtimeClass"`
	WorkerID      string `json:"workerId"`
	PID           int    `json:"pid"`
	ConsolePort   int    `json:"consolePort,omitempty"`
	AdbPort       int    `json:"adbPort,omitempty"`
	GRPCPort      int    `json:"grpcPort,omitempty"`
	WorkerPID     int    `json:"workerPid,omitempty"`
	WorkerBaseURL string `json:"workerBaseUrl,omitempty"`
	// DataDir is the per-instance writable directory the runtime adapter
	// pointed the emulator's -datadir at (isolating this instance's
	// userdata-qemu.img/cache.img/sdcard.img from every other instance of
	// the same template) — informational only, reported up to
	// instances.state_data_ref for operator visibility.
	DataDir string `json:"dataDir,omitempty"`
}

type InstanceStatus struct {
	State     string         `json:"state"`
	Handle    InstanceHandle `json:"handle"`
	LastError string         `json:"lastError,omitempty"`
}

type TransportSessionRequest struct {
	SessionID     string         `json:"sessionId"`
	AttachmentID  string         `json:"attachmentId"`
	InstanceID    string         `json:"instanceId"`
	RuntimeClass  string         `json:"runtimeClass"`
	Role          string         `json:"role"`
	RuntimeHandle InstanceHandle `json:"runtimeHandle"`
	WorkerBaseURL string         `json:"workerBaseUrl"`
}

type TransportSession struct {
	SessionID     string `json:"sessionId"`
	AttachmentID  string `json:"attachmentId"`
	InstanceID    string `json:"instanceId"`
	RuntimeClass  string `json:"runtimeClass"`
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	WorkerBaseURL string `json:"workerBaseUrl"`
	SessionToken  string `json:"sessionToken"`
}

type Adapter interface {
	RuntimeClass() string
	DisplayName() string
	DiscoverTemplates() ([]Template, error)
	Start(StartRequest) (InstanceHandle, error)
	Inspect(instanceID string) (InstanceStatus, error)
	Stop(instanceID string) (InstanceStatus, error)
	Reconcile() ([]InstanceStatus, error)
	CreateTransportSession(TransportSessionRequest) (TransportSession, error)
	// CleanupInstanceData removes any on-disk state private to instanceID
	// that outlives Stop (e.g. androidstudio's per-instance cloned AVD) —
	// called once, when the instance itself is deleted, not on every stop
	// (stopped instances keep their data so a later restart resumes it).
	// Best-effort/idempotent: a no-op (nil error) if there's nothing to
	// clean up, whether because this adapter has no such concept or
	// because it was already removed.
	CleanupInstanceData(instanceID string) error
	// CaptureBaseImage snapshots sourceDataDir (an instance's own
	// state_data_ref content directory — the caller is responsible for
	// confirming that instance is stopped first) into a new immutable
	// artifact directory keyed by baseImageID, returning its path for the
	// control plane to persist as base_images.artifact_path.
	CaptureBaseImage(baseImageID, sourceDataDir string) (artifactPath string, err error)
}
