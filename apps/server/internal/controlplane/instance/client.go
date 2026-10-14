package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	workerapi "mako/internal/worker/api"
	"mako/internal/worker/runtime"
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

// WorkerClient talks to the mako-worker process serving one instance.
// Phase 1 only needs template discovery and instance lifecycle;
// transport-session methods belong to Phase 2 (session/transport), added to
// this same interface when that work lands — not a separate client type.
type WorkerClient interface {
	ListTemplates(ctx context.Context) ([]runtime.Template, error)
	StartInstance(ctx context.Context, req workerapi.StartInstanceRequest) (workerapi.StartInstanceResponse, error)
	InspectInstance(ctx context.Context, instanceID string) (workerapi.InspectInstanceResponse, error)
	StopInstance(ctx context.Context, instanceID string) (workerapi.StopInstanceResponse, error)
}

// HTTPWorkerClient is the only WorkerClient implementation: a plain HTTP
// client against one worker's base URL, matching the wire shapes the worker
// itself already defines in mako/internal/worker/api — reused directly
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
