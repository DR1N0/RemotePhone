package webrtc

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"mako-worker/internal/transport"
)

// Adapter implements transport.Adapter using pion/webrtc.
type Adapter struct {
	instanceID   string
	attachmentID string
	role         string
	adbSerial    string
	screenW      int
	screenH      int
	stunURL      string

	pc      *webrtc.PeerConnection
	capture *ScreenCapture
}

// NewAdapter creates a WebRTCAdapter. screenW/H are the emulator display pixels.
func NewAdapter(instanceID, attachmentID, role, adbSerial string, screenW, screenH int, stunURL string) *Adapter {
	return &Adapter{
		instanceID:   instanceID,
		attachmentID: attachmentID,
		role:         role,
		adbSerial:    adbSerial,
		screenW:      screenW,
		screenH:      screenH,
		stunURL:      stunURL,
	}
}

func (a *Adapter) HandleOffer(instanceID, attachmentID, role, sdpOffer string) (string, error) {
	se := webrtc.SettingEngine{}
	api := webrtc.NewAPI(webrtc.WithSettingEngine(se))

	config := webrtc.Configuration{}
	if a.stunURL != "" {
		config.ICEServers = []webrtc.ICEServer{{URLs: []string{a.stunURL}}}
	}

	pc, err := api.NewPeerConnection(config)
	if err != nil {
		return "", fmt.Errorf("new peer connection: %w", err)
	}
	a.pc = pc

	// Add H264 video track.
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "mako-screen",
	)
	if err != nil {
		return "", fmt.Errorf("create video track: %w", err)
	}
	if _, err = pc.AddTrack(videoTrack); err != nil {
		return "", fmt.Errorf("add video track: %w", err)
	}

	// Add bidirectional data channel for input.
	dc, err := pc.CreateDataChannel("input", nil)
	if err != nil {
		return "", fmt.Errorf("create data channel: %w", err)
	}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var ev transport.InputEvent
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			return
		}
		_ = ForwardInput(a.adbSerial, ev, a.role, a.screenW, a.screenH)
	})

	if err = pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdpOffer,
	}); err != nil {
		return "", fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return "", fmt.Errorf("create answer: %w", err)
	}
	if err = pc.SetLocalDescription(answer); err != nil {
		return "", fmt.Errorf("set local description: %w", err)
	}

	// Start ADB H264 capture and pipe into video track.
	cap, stdout, err := StartCapture(a.adbSerial)
	if err != nil {
		return "", fmt.Errorf("start capture: %w", err)
	}
	a.capture = cap
	go pipeH264(stdout, videoTrack)

	return answer.SDP, nil
}

func (a *Adapter) HandleICECandidate(candidate string) error {
	if a.pc == nil {
		return fmt.Errorf("peer connection not initialized")
	}
	return a.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate})
}

func (a *Adapter) HandleInput(ev transport.InputEvent) error {
	return ForwardInput(a.adbSerial, ev, a.role, a.screenW, a.screenH)
}

func (a *Adapter) Stop() error {
	if a.capture != nil {
		a.capture.Stop()
	}
	if a.pc != nil {
		return a.pc.Close()
	}
	return nil
}

// pipeH264 reads raw H264 NAL units from r and writes them as RTP samples.
func pipeH264(r io.Reader, track *webrtc.TrackLocalStaticSample) {
	buf := make([]byte, 1500)
	for {
		n, err := r.Read(buf)
		if err != nil {
			return
		}
		_ = track.WriteSample(media.Sample{
			Data:     append([]byte{}, buf[:n]...),
			Duration: time.Second / 30, // ~30fps frame duration
		})
	}
}
