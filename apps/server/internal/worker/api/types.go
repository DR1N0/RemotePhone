package api

import "mako/internal/worker/runtime"

type HealthResponse struct {
	Status            string                `json:"status"`
	WorkerID          string                `json:"workerId,omitempty"`
	HostOS            string                `json:"hostOs,omitempty"`
	Arch              string                `json:"arch,omitempty"`
	InstalledRuntimes []runtime.AdapterInfo `json:"installedRuntimes,omitempty"`
}

type TemplateListResponse struct {
	Templates []runtime.Template `json:"templates"`
}

type StartInstanceRequest struct {
	InstanceID            string               `json:"instanceId"`
	RuntimeClass          string               `json:"runtimeClass"`
	TemplateID            string               `json:"templateId"`
	LaunchConfig          runtime.LaunchConfig `json:"launchConfig"`
	BaseImageArtifactPath string               `json:"baseImageArtifactPath,omitempty"`
}

type StartInstanceResponse struct {
	State  string                 `json:"state"`
	Handle runtime.InstanceHandle `json:"handle"`
}

// AdoptInstanceRequest's Handle carries everything InstanceHandle already
// has — the control plane forwards its persisted runtime_handle_json
// almost verbatim after spawning a replacement worker process.
type AdoptInstanceRequest struct {
	Handle runtime.InstanceHandle `json:"handle"`
}

type AdoptInstanceResponse struct {
	Handle runtime.InstanceHandle `json:"handle"`
}

type InspectInstanceResponse struct {
	State     string                 `json:"state"`
	Handle    runtime.InstanceHandle `json:"handle"`
	LastError string                 `json:"lastError,omitempty"`
}

type StopInstanceResponse struct {
	State     string                 `json:"state"`
	Handle    runtime.InstanceHandle `json:"handle"`
	LastError string                 `json:"lastError,omitempty"`
}

// CleanupInstanceDataRequest's RuntimeClass is required explicitly: the
// worker process handling this call is typically short-lived and spawned
// just for this purpose (see Registry.CleanupInstanceData), with no prior
// in-memory knowledge of which adapter owns this instance ID.
type CleanupInstanceDataRequest struct {
	RuntimeClass string `json:"runtimeClass"`
}

// CaptureBaseImageRequest's SourceDataDir is the source instance's own
// state_data_ref — the caller (control plane) is responsible for confirming
// that instance is stopped before calling this.
type CaptureBaseImageRequest struct {
	RuntimeClass  string `json:"runtimeClass"`
	BaseImageID   string `json:"baseImageId"`
	SourceDataDir string `json:"sourceDataDir"`
}

type CaptureBaseImageResponse struct {
	ArtifactPath string `json:"artifactPath"`
}

type CreateTransportSessionRequest struct {
	SessionID     string                 `json:"sessionId"`
	AttachmentID  string                 `json:"attachmentId"`
	InstanceID    string                 `json:"instanceId"`
	RuntimeClass  string                 `json:"runtimeClass"`
	Role          string                 `json:"role"`
	RuntimeHandle runtime.InstanceHandle `json:"runtimeHandle"`
}

type UpdateTransportSessionRoleRequest struct {
	InstanceID   string `json:"instanceId"`
	AttachmentID string `json:"attachmentId"`
	Role         string `json:"role"`
}

type TransportSessionResponse struct {
	SessionID     string `json:"sessionId"`
	AttachmentID  string `json:"attachmentId"`
	InstanceID    string `json:"instanceId"`
	RuntimeClass  string `json:"runtimeClass"`
	Mode          string `json:"mode"`
	Status        string `json:"status"`
	WorkerBaseURL string `json:"workerBaseUrl"`
	SessionToken  string `json:"sessionToken"`
}

type HostInventory struct {
	HostOS string
	Arch   string
}

type TransportOfferRequest struct {
	InstanceID   string `json:"instanceId"`
	AttachmentID string `json:"attachmentId"`
	SdpOffer     string `json:"sdpOffer"`
}

type TransportOfferResponse struct {
	SdpAnswer string `json:"sdpAnswer"`
}

type ICECandidateRequest struct {
	InstanceID   string `json:"instanceId"`
	AttachmentID string `json:"attachmentId"`
	Candidate    string `json:"candidate"`
}
