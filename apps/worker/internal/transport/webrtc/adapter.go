package webrtc

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
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

	forwardInput func(string, transport.InputEvent, string, int, int) error

	mu      sync.Mutex
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
		forwardInput: ForwardInput,
	}
}

func (a *Adapter) HandleOffer(instanceID, attachmentID, sdpOffer string) (string, error) {
	// Stop any existing resources to avoid leaks on re-offer.
	a.mu.Lock()
	oldCap := a.capture
	oldPc := a.pc
	a.capture = nil
	a.pc = nil
	a.mu.Unlock()
	if oldCap != nil {
		oldCap.Stop()
	}
	if oldPc != nil {
		_ = oldPc.Close()
	}

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

	// Add H264 video track.
	videoTrack, err := webrtc.NewTrackLocalStaticSample(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeH264},
		"video", "mako-screen",
	)
	if err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("create video track: %w", err)
	}
	if _, err = pc.AddTrack(videoTrack); err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("add video track: %w", err)
	}

	// Chrome creates the input data channel as part of its offer.
	pc.OnDataChannel(a.handleDataChannel)
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			a.stopCaptureForPeerConnection(pc)
		}
	})

	if err = pc.SetRemoteDescription(webrtc.SessionDescription{
		Type: webrtc.SDPTypeOffer,
		SDP:  sdpOffer,
	}); err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("set remote description: %w", err)
	}

	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("create answer: %w", err)
	}
	if err = pc.SetLocalDescription(answer); err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("set local description: %w", err)
	}

	// Start ADB H264 capture and pipe into video track.
	cap, stdout, err := StartCapture(a.adbSerial)
	if err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("start capture: %w", err)
	}

	a.mu.Lock()
	a.pc = pc
	a.capture = cap
	a.mu.Unlock()

	go pipeH264(stdout, videoTrack)

	if err := waitForGathering(webrtc.GatheringCompletePromise(pc), 10*time.Second); err != nil {
		a.stopPeerConnection(pc)
		return "", err
	}
	return pc.LocalDescription().SDP, nil
}

func (a *Adapter) handleDataChannel(dc *webrtc.DataChannel) {
	if dc.Label() != "input" {
		return
	}
	dc.OnMessage(a.handleInputMessage)
}

func (a *Adapter) handleInputMessage(msg webrtc.DataChannelMessage) {
	var event transport.InputEvent
	if err := json.Unmarshal(msg.Data, &event); err != nil {
		return
	}
	_ = a.handleInputEvent(event)
}

func waitForGathering(gathering <-chan struct{}, timeout time.Duration) error {
	select {
	case <-gathering:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("gather ICE candidates: timeout")
	}
}

func (a *Adapter) HandleICECandidate(candidate string) error {
	a.mu.Lock()
	pc := a.pc
	a.mu.Unlock()
	if pc == nil {
		return fmt.Errorf("peer connection not initialized")
	}
	return pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: candidate})
}

func (a *Adapter) HandleInput(ev transport.InputEvent) error {
	return a.handleInputEvent(ev)
}

func (a *Adapter) handleInputEvent(ev transport.InputEvent) error {
	return a.forwardInput(a.adbSerial, ev, a.Role(), a.screenW, a.screenH)
}

func (a *Adapter) SetRole(role string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.role = role
}

func (a *Adapter) Role() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.role
}

func (a *Adapter) stopCaptureForPeerConnection(pc *webrtc.PeerConnection) {
	a.mu.Lock()
	if a.pc != pc {
		a.mu.Unlock()
		return
	}
	cap := a.capture
	a.capture = nil
	a.pc = nil
	a.mu.Unlock()
	if cap != nil {
		cap.Stop()
	}
}

func (a *Adapter) stopPeerConnection(pc *webrtc.PeerConnection) {
	a.mu.Lock()
	if a.pc != pc {
		a.mu.Unlock()
		return
	}
	cap := a.capture
	a.capture = nil
	a.pc = nil
	a.mu.Unlock()
	if cap != nil {
		cap.Stop()
	}
	_ = pc.Close()
}

func (a *Adapter) Stop() error {
	a.mu.Lock()
	cap := a.capture
	pc := a.pc
	a.capture = nil
	a.pc = nil
	a.mu.Unlock()
	if cap != nil {
		cap.Stop()
	}
	if pc != nil {
		return pc.Close()
	}
	return nil
}

// pipeH264 reads an Annex-B H264 byte stream from r, splits on NAL unit
// start codes (0x00 0x00 0x01 or 0x00 0x00 0x00 0x01), and writes each
// complete NAL unit as one RTP sample.
func pipeH264(r io.Reader, track *webrtc.TrackLocalStaticSample) {
	var buf []byte
	tmp := make([]byte, 4096)

	emit := func(nal []byte) {
		if len(nal) == 0 {
			return
		}
		_ = track.WriteSample(media.Sample{
			Data:     append([]byte{}, nal...),
			Duration: time.Second / 30,
		})
	}

	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
			// Scan for NAL unit boundaries
			for {
				idx := findStartCode(buf)
				if idx < 0 {
					break
				}
				if idx > 0 {
					emit(buf[:idx])
				}
				// Skip past the start code
				if idx+4 <= len(buf) && buf[idx+3] == 0x01 {
					buf = buf[idx+4:] // 4-byte start code
				} else if idx+3 <= len(buf) {
					buf = buf[idx+3:] // 3-byte start code
				} else {
					buf = buf[idx:]
					break
				}
			}
		}
		if err != nil {
			emit(buf) // emit any remaining bytes
			return
		}
	}
}

// findStartCode returns the index of the next Annex-B start code
// (0x00 0x00 0x01 or 0x00 0x00 0x00 0x01) in b, or -1 if not found.
func findStartCode(b []byte) int {
	for i := 0; i+2 < len(b); i++ {
		if b[i] == 0x00 && b[i+1] == 0x00 {
			if b[i+2] == 0x01 {
				return i
			}
			if i+3 < len(b) && b[i+2] == 0x00 && b[i+3] == 0x01 {
				return i
			}
		}
	}
	return -1
}
