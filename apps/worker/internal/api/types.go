package api

import "mako-worker/internal/runtime"

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
	InstanceID   string               `json:"instanceId"`
	RuntimeClass string               `json:"runtimeClass"`
	TemplateID   string               `json:"templateId"`
	LaunchConfig runtime.LaunchConfig `json:"launchConfig"`
}

type StartInstanceResponse struct {
	State  string                 `json:"state"`
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
	Role         string `json:"role"`
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
