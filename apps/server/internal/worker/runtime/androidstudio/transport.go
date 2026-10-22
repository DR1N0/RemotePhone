package androidstudio

import (
	"fmt"
	"strings"

	"mako/internal/worker/runtime"
	webrtctransport "mako/internal/worker/transport/webrtc"
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
	// AVD profiles vary their actual resolution (e.g. Medium_Phone_API_36.1
	// is 1080x2400) — ask the device rather than assuming a fixed default.
	screenW, screenH := webrtctransport.QueryScreenSize(adbSerial, 1080, 1920)
	// The emulator is already launched with -grpc <port> (see launch.go) —
	// this is its own gRPC server, used as the video capture frame source.
	grpcAddr := fmt.Sprintf("127.0.0.1:%d", req.RuntimeHandle.GRPCPort)

	candidate := webrtctransport.NewAdapter(
		req.InstanceID, req.AttachmentID, req.Role,
		adbSerial, screenW, screenH, a.stunURL, grpcAddr, a.ffmpegPath,
	)
	candidate.SetBitRate(a.streamBitRate)
	candidate.SetHostIP(a.webRTCHostIP)
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
