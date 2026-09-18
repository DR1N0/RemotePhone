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
	}
}

func (a *Adapter) HandleOffer(instanceID, attachmentID, role, sdpOffer string) (string, error) {
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

	// Add bidirectional data channel for input.
	dc, err := pc.CreateDataChannel("input", nil)
	if err != nil {
		_ = pc.Close()
		return "", fmt.Errorf("create data channel: %w", err)
	}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		var ev transport.InputEvent
		if err := json.Unmarshal(msg.Data, &ev); err != nil {
			return
		}
		_ = ForwardInput(a.adbSerial, ev, a.Role(), a.screenW, a.screenH)
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

	return answer.SDP, nil
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
	return ForwardInput(a.adbSerial, ev, a.Role(), a.screenW, a.screenH)
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
