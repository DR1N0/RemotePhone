package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	workerapi "remotephone/internal/worker/api"
	"remotephone/internal/worker/runtime"
)

// ErrWorkerRequestFailed wraps a non-2xx response from a worker, carrying
// the HTTP status so callers can distinguish e.g. a 404 (unknown instance)
// from a 500. This replaces worker-client.ts's ad hoc per-method error
// strings ("Worker request failed with status …", "Worker ICE relay failed
// with status …") with one typed error every call site can inspect via
// errors.As, instead of string-matching distinct messages per method.
type ErrWorkerRequestFailed struct {
	StatusCode int
}

func (e *ErrWorkerRequestFailed) Error() string {
	return fmt.Sprintf("worker request failed with status %d", e.StatusCode)
}

// WorkerClient talks to the remotephone-worker process serving one instance.
// Phase 2's transport package needs the transport-session/signaling methods
// below in addition to Phase 1's template discovery and instance lifecycle —
// added to this same interface rather than a separate client type, matching
// worker-client.ts's single ManagedInstanceWorkerClient interface.
type WorkerClient interface {
	ListTemplates(ctx context.Context) ([]runtime.Template, error)
	StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error)
	InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error)
	StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error)
	// AdoptInstance tells a freshly-spawned replacement worker process to
	// pick up an instance it didn't itself start — Phase 4's worker-liveness
	// respawn path. See docs/plans/worker-lifecycle.md.
	AdoptInstance(ctx context.Context, instanceID string, handle runtime.InstanceHandle) error
	// CleanupInstanceData removes an already-deleted instance's on-disk
	// private state (e.g. androidstudio's cloned AVD) — called once at
	// instance deletion, not on every stop.
	CleanupInstanceData(ctx context.Context, instanceID, runtimeClass string) error
	// CaptureBaseImage snapshots sourceDataDir (a stopped instance's own
	// state_data_ref) into a new artifact directory keyed by baseImageID,
	// returning its path.
	CaptureBaseImage(ctx context.Context, baseImageID, sourceDataDir, runtimeClass string) (artifactPath string, err error)
	CreateTransportSession(ctx context.Context, req workerapi.CreateTransportSessionRequest) (workerapi.TransportSessionResponse, error)
	UpdateTransportRole(ctx context.Context, req workerapi.UpdateTransportSessionRoleRequest) error
	DestroyTransportSession(ctx context.Context, instanceID, attachmentID string) error
	ForwardOffer(ctx context.Context, req workerapi.TransportOfferRequest) (workerapi.TransportOfferResponse, error)
	ForwardICECandidate(ctx context.Context, req workerapi.ICECandidateRequest) error
}

// HTTPWorkerClient is the only WorkerClient implementation: a plain HTTP
// client against one worker's base URL, matching the wire shapes the worker
// itself already defines in remotephone/internal/worker/api — reused directly
// rather than duplicated, since both sides live in the same Go module.
type HTTPWorkerClient struct {
	baseURL string
	client  *http.Client
}

func NewHTTPWorkerClient(baseURL string) *HTTPWorkerClient {
	return &HTTPWorkerClient{baseURL: baseURL, client: http.DefaultClient}
}

func (c *HTTPWorkerClient) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("worker request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &ErrWorkerRequestFailed{StatusCode: resp.StatusCode}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode worker response: %w", err)
	}
	return nil
}

func (c *HTTPWorkerClient) ListTemplates(ctx context.Context) ([]runtime.Template, error) {
	var out workerapi.TemplateListResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/templates", nil, &out); err != nil {
		return nil, err
	}
	return out.Templates, nil
}

func (c *HTTPWorkerClient) StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error) {
	var out workerapi.StartInstanceResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/instances/start", req, &out); err != nil {
		return workerapi.StartInstanceResponse{}, err
	}
	return out, nil
}

func (c *HTTPWorkerClient) InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error) {
	var out workerapi.InspectInstanceResponse
	if err := c.doJSON(ctx, http.MethodGet, "/v1/instances/"+url.PathEscape(instanceID), nil, &out); err != nil {
		return workerapi.InspectInstanceResponse{}, err
	}
	return out, nil
}

func (c *HTTPWorkerClient) StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error) {
	var out workerapi.StopInstanceResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(instanceID)+"/stop", nil, &out); err != nil {
		return workerapi.StopInstanceResponse{}, err
	}
	return out, nil
}

func (c *HTTPWorkerClient) AdoptInstance(ctx context.Context, instanceID string, handle runtime.InstanceHandle) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(instanceID)+"/adopt", workerapi.AdoptInstanceRequest{Handle: handle}, nil)
}

func (c *HTTPWorkerClient) CleanupInstanceData(ctx context.Context, instanceID, runtimeClass string) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/instances/"+url.PathEscape(instanceID)+"/cleanup-data", workerapi.CleanupInstanceDataRequest{RuntimeClass: runtimeClass}, nil)
}

func (c *HTTPWorkerClient) CaptureBaseImage(ctx context.Context, baseImageID, sourceDataDir, runtimeClass string) (string, error) {
	var out workerapi.CaptureBaseImageResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/base-images/capture", workerapi.CaptureBaseImageRequest{
		RuntimeClass:  runtimeClass,
		BaseImageID:   baseImageID,
		SourceDataDir: sourceDataDir,
	}, &out); err != nil {
		return "", err
	}
	return out.ArtifactPath, nil
}

func (c *HTTPWorkerClient) CreateTransportSession(ctx context.Context, req workerapi.CreateTransportSessionRequest) (workerapi.TransportSessionResponse, error) {
	var out workerapi.TransportSessionResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/transport/session", req, &out); err != nil {
		return workerapi.TransportSessionResponse{}, err
	}
	return out, nil
}

func (c *HTTPWorkerClient) UpdateTransportRole(ctx context.Context, req workerapi.UpdateTransportSessionRoleRequest) error {
	return c.doJSON(ctx, http.MethodPatch, "/v1/transport/session/role", req, nil)
}

func (c *HTTPWorkerClient) DestroyTransportSession(ctx context.Context, instanceID, attachmentID string) error {
	query := url.Values{"instanceId": {instanceID}, "attachmentId": {attachmentID}}
	return c.doJSON(ctx, http.MethodDelete, "/v1/transport/session?"+query.Encode(), nil, nil)
}

func (c *HTTPWorkerClient) ForwardOffer(ctx context.Context, req workerapi.TransportOfferRequest) (workerapi.TransportOfferResponse, error) {
	var out workerapi.TransportOfferResponse
	if err := c.doJSON(ctx, http.MethodPost, "/v1/transport/offer", req, &out); err != nil {
		return workerapi.TransportOfferResponse{}, err
	}
	return out, nil
}

func (c *HTTPWorkerClient) ForwardICECandidate(ctx context.Context, req workerapi.ICECandidateRequest) error {
	return c.doJSON(ctx, http.MethodPost, "/v1/transport/ice-candidate", req, nil)
}
