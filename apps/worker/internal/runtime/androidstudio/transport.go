package androidstudio

import (
	"fmt"
	"strings"

	"mako-worker/internal/runtime"
)

func (a *Adapter) CreateTransportSession(req runtime.TransportSessionRequest) (runtime.TransportSession, error) {
	if strings.TrimSpace(req.SessionID) == "" {
		return runtime.TransportSession{}, fmt.Errorf("sessionId is required")
	}
	if strings.TrimSpace(req.AttachmentID) == "" {
		return runtime.TransportSession{}, fmt.Errorf("attachmentId is required")
	}
	if strings.TrimSpace(req.InstanceID) == "" {
		return runtime.TransportSession{}, fmt.Errorf("instanceId is required")
	}
	if strings.TrimSpace(req.RuntimeClass) == "" {
		return runtime.TransportSession{}, fmt.Errorf("runtimeClass is required")
	}
	if strings.TrimSpace(req.WorkerBaseURL) == "" {
		return runtime.TransportSession{}, fmt.Errorf("workerBaseUrl is required")
	}

	return runtime.TransportSession{
		SessionID:     req.SessionID,
		AttachmentID:  req.AttachmentID,
		InstanceID:    req.InstanceID,
		RuntimeClass:  req.RuntimeClass,
		Mode:          "prototype-webrtc",
		Status:        "stub",
		WorkerBaseURL: req.WorkerBaseURL,
		SessionToken:  fmt.Sprintf("stub-%s-%s", req.SessionID, req.AttachmentID),
	}, nil
}
