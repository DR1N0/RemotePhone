package androidstudio

import (
	"fmt"
	"strings"

	"mako-worker/internal/runtime"
	webrtctransport "mako-worker/internal/transport/webrtc"
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

	adbSerial := fmt.Sprintf("emulator-%d", req.RuntimeHandle.ConsolePort)
	// Default screen dimensions for standard AVD profiles.
	const screenW, screenH = 1080, 1920

	candidate := webrtctransport.NewAdapter(
		req.InstanceID, req.AttachmentID, req.Role,
		adbSerial, screenW, screenH, a.stunURL,
	)
	if a.transportStore != nil {
		key := req.InstanceID + ":" + req.AttachmentID
		adapter, inserted := a.transportStore.GetOrPut(key, candidate)
		adapter.SetRole(req.Role)
		if !inserted {
			_ = candidate.Stop()
		}
	}

	return runtime.TransportSession{
		SessionID:     req.SessionID,
		AttachmentID:  req.AttachmentID,
		InstanceID:    req.InstanceID,
		RuntimeClass:  req.RuntimeClass,
		Mode:          "webrtc",
		Status:        "ready",
		WorkerBaseURL: req.WorkerBaseURL,
		SessionToken:  fmt.Sprintf("webrtc-%s-%s", req.SessionID, req.AttachmentID),
	}, nil
}
