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
}

type InstanceHandle struct {
	InstanceID   string `json:"instanceId"`
	TemplateID   string `json:"templateId"`
	RuntimeClass string `json:"runtimeClass"`
	WorkerID     string `json:"workerId"`
	PID          int    `json:"pid"`
	ConsolePort  int    `json:"consolePort,omitempty"`
	AdbPort      int    `json:"adbPort,omitempty"`
	GRPCPort     int    `json:"grpcPort,omitempty"`
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
}
