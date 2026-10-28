package instance

import (
	"fmt"
	"strings"

	"remotephone/internal/controlplane/placement"
	"remotephone/internal/worker/runtime"
)

// ResourceProfile is a control-plane-only request concept the worker never
// sees (its StartInstanceRequest takes only runtimeClass/templateId/
// launchConfig) — unlike LaunchConfig, which reuses runtime.LaunchConfig
// directly since its shape is identical to what the worker actually
// consumes. Placement policy lives in the placement package, not here.
type ResourceProfile struct {
	CPUMillicores    int    `json:"cpuMillicores"`
	MemoryMB         int    `json:"memoryMb"`
	DiskMB           int    `json:"diskMb"`
	AccelerationMode string `json:"accelerationMode"`
	StreamProfile    string `json:"streamProfile"`
}

type CreateInstanceRequest struct {
	Name            string               `json:"name"`
	TemplateID      string               `json:"templateId"`
	Placement       placement.Policy     `json:"placement"`
	ResourceProfile ResourceProfile      `json:"resourceProfile"`
	LaunchConfig    runtime.LaunchConfig `json:"launchConfig"`
}

// Validate checks request shape only (required fields, known enum values) —
// it deliberately does NOT check whether Placement.Kind is actually
// supported; that's Placement.Resolve's job (a different, later failure
// mode), mirroring the TS code's own split between schema-shape validation
// (domain/schemas.ts's zod schema) and placement-support validation
// (placement-service.ts).
func (r CreateInstanceRequest) Validate() error {
	if strings.TrimSpace(r.Name) == "" {
		return fmt.Errorf("%w: name is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(r.TemplateID) == "" {
		return fmt.Errorf("%w: templateId is required", ErrInvalidRequest)
	}
	switch r.Placement.Kind {
	case "local-only", "best-fit":
	case "specific-worker":
		if strings.TrimSpace(r.Placement.WorkerID) == "" {
			return fmt.Errorf("%w: placement.workerId is required for kind specific-worker", ErrInvalidRequest)
		}
	default:
		return fmt.Errorf("%w: unknown placement kind %q", ErrInvalidRequest, r.Placement.Kind)
	}
	if r.ResourceProfile.CPUMillicores <= 0 || r.ResourceProfile.MemoryMB <= 0 || r.ResourceProfile.DiskMB <= 0 {
		return fmt.Errorf("%w: resourceProfile cpuMillicores, memoryMb, and diskMb must be positive", ErrInvalidRequest)
	}
	switch r.ResourceProfile.AccelerationMode {
	case "forbid", "prefer", "require", "auto":
	default:
		return fmt.Errorf("%w: invalid resourceProfile.accelerationMode %q", ErrInvalidRequest, r.ResourceProfile.AccelerationMode)
	}
	switch r.ResourceProfile.StreamProfile {
	case "prototype", "balanced", "high":
	default:
		return fmt.Errorf("%w: invalid resourceProfile.streamProfile %q", ErrInvalidRequest, r.ResourceProfile.StreamProfile)
	}
	return nil
}

// ManagedInstance is the API-facing representation of an instances row —
// the allowlist mapping every DB row goes through before reaching a
// response body (no ad hoc field copying at call sites).
type ManagedInstance struct {
	ID              string                  `json:"id"`
	Name            string                  `json:"name"`
	OwnerID         string                  `json:"ownerId"`
	RuntimeClass    string                  `json:"runtimeClass"`
	TemplateID      string                  `json:"templateId"`
	Placement       placement.Policy        `json:"placement"`
	ResourceProfile ResourceProfile         `json:"resourceProfile"`
	LaunchConfig    runtime.LaunchConfig    `json:"launchConfig"`
	DesiredState    string                  `json:"desiredState"`
	ObservedState   string                  `json:"observedState"`
	RuntimeHandle   *runtime.InstanceHandle `json:"runtimeHandle,omitempty"`
	StateDataRef    *string                 `json:"stateDataRef,omitempty"`
	LastError       *string                 `json:"lastError,omitempty"`
	CreatedAt       string                  `json:"createdAt"`
	UpdatedAt       string                  `json:"updatedAt"`
}

// TemplateInfo is the client-facing subset of a worker-discovered template.
// Deliberately excludes runtime.Template.Metadata, which carries host-local
// paths (AVD directory, emulator binary) that have no business crossing the
// client API boundary.
type TemplateInfo struct {
	TemplateID   string `json:"templateId"`
	DisplayName  string `json:"displayName"`
	RuntimeClass string `json:"runtimeClass"`
}
