package webrtc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v3"
	"github.com/pion/webrtc/v3/pkg/media"
	"mako/internal/worker/transport"
	"mako/internal/worker/transport/emugrpc"
)

// captureFPS/captureBitRate tune the encoder for latency over quality — see
// buildFFmpegArgs's doc comment for the per-codec flags this drives.
const (
	captureFPS     = 30
	captureBitRate = 2_000_000
)

// Adapter implements transport.Adapter using pion/webrtc.
type Adapter struct {
	instanceID   string
	attachmentID string
	role         string
	adbSerial    string // still used for input injection (input.go) — unrelated to video capture
	screenW      int
	screenH      int
	stunURL      string
	grpcAddr     string // the emulator's own gRPC server, e.g. "127.0.0.1:8554"
	ffmpegPath   string

	forwardInput   func(string, transport.InputEvent, string, int, int) error
	newFrameSource func(addr string, width, height int) emugrpc.FrameSource
	startEncoder   func(ffmpegPath, codec string, opts EncodeOptions) (encoderProcess, error)
	selectEncoder  func(ctx context.Context, explicitFFmpegPath string, width, height int) (ffmpegPath, codec string, err error)

	mu      sync.Mutex
	pc      *webrtc.PeerConnection
	capture *captureSession
}

// halveForCapture halves n and rounds down to an even number (ffmpeg's
// rawvideo input needs even dimensions for common pixel formats), or
// returns n unchanged if halving would produce 0 — input events still map
// against a.screenW/H, the device's real resolution (see input.go), so
// downscaling the video stream doesn't affect touch accuracy.
func halveForCapture(n int) int {
	if n <= 1 {
		return n
	}
	half := n / 2
	return half - half%2
}

// NewAdapter creates a WebRTCAdapter. screenW/H are the emulator display
// pixels (used for input coordinate mapping); grpcAddr is the emulator's own
// gRPC server address (already running — see runtime/androidstudio/launch.go
// — used as the video capture frame source); ffmpegPath is resolved via
// ResolveFFmpegBinary by the caller.
func NewAdapter(instanceID, attachmentID, role, adbSerial string, screenW, screenH int, stunURL, grpcAddr, ffmpegPath string) *Adapter {
	return &Adapter{
		instanceID:   instanceID,
		attachmentID: attachmentID,
		role:         role,
		adbSerial:    adbSerial,
		screenW:      screenW,
		screenH:      screenH,
		stunURL:      stunURL,
		grpcAddr:     grpcAddr,
		ffmpegPath:   ffmpegPath,
		forwardInput: ForwardInput,
		newFrameSource: func(addr string, width, height int) emugrpc.FrameSource {
			return &emugrpc.GRPCFrameSource{Addr: addr, Width: width, Height: height}
		},
		startEncoder: func(ffmpegPath, codec string, opts EncodeOptions) (encoderProcess, error) {
			return StartEncoder(ffmpegPath, codec, opts)
		},
		selectEncoder: cachedSelectEncoder,
	}
}

// captureSession bundles one HandleOffer call's video capture pipeline:
// the emulator gRPC frame source, the ffmpeg encoder, and the pacer
// bridging the two. Stop tears all three down together.
type captureSession struct {
	cancel  context.CancelFunc
	encoder encoderProcess
	pacer   *Pacer
}

func (c *captureSession) Stop() {
	if c == nil {
		return
	}
	c.cancel()
	if c.pacer != nil {
		c.pacer.Stop()
	}
	if c.encoder != nil {
		c.encoder.Stop()
	}
}

// startVideoCapture wires together this connection's video pipeline:
// gRPC streamScreenshot (emugrpc.FrameSource) -> Pacer (bridges the
// event-driven frame source into ffmpeg's fixed-rate rawvideo input) ->
// ffmpeg hardware H264 encoder -> pipeH264 (unchanged NAL parsing into
// videoTrack). Returns a descriptive error on any failure — there is no
// fallback to a degraded capture path; see HandleOffer's call site comment.
func (a *Adapter) startVideoCapture(pc *webrtc.PeerConnection, videoTrack *webrtc.TrackLocalStaticSample) (*captureSession, error) {
	captureW, captureH := halveForCapture(a.screenW), halveForCapture(a.screenH)
	log.Printf("capture: starting for instance=%s at %dx%d via grpc=%s", a.instanceID, captureW, captureH, a.grpcAddr)

	ctx, cancel := context.WithCancel(context.Background())

	frameSource := a.newFrameSource(a.grpcAddr, captureW, captureH)
	frames, err := frameSource.Frames(ctx)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("video capture unavailable: emulator gRPC unreachable at %s: %w", a.grpcAddr, err)
	}

	ffmpegPath, codec, err := a.selectEncoder(ctx, a.ffmpegPath, captureW, captureH)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("video capture unavailable: no working H264 encoder found: %w", err)
	}
	log.Printf("capture: selected encoder %s for instance=%s at %dx%d", codec, a.instanceID, captureW, captureH)

	encoder, err := a.startEncoder(ffmpegPath, codec, EncodeOptions{
		Width:   captureW,
		Height:  captureH,
		FPS:     captureFPS,
		BitRate: captureBitRate,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("video capture unavailable: start %s encoder: %w", codec, err)
	}

	session := &captureSession{cancel: cancel, encoder: encoder}
	session.pacer = NewPacer(encoder, captureFPS, func(stallErr error) {
		log.Printf("capture[%s]: encoder stalled, closing connection: %v", a.instanceID, stallErr)
		a.stopPeerConnection(pc)
	})

	// RGBA8888 is 4 bytes/pixel — ffmpeg's rawvideo demuxer has no in-band
	// framing, so every frame fed to it must be exactly this many bytes or
	// the stream desyncs. A frame at an unexpected size (e.g. the emulator's
	// aspect-ratio-preserving scaling didn't land exactly on captureW x
	// captureH) is dropped rather than risking a corrupted encode.
	expectedBytes := captureW * captureH * 4
	go func() {
		for frame := range frames {
			if len(frame.Data) != expectedBytes {
				log.Printf("capture[%s]: dropping frame with size %d, want %d for %dx%d",
					a.instanceID, len(frame.Data), expectedBytes, captureW, captureH)
				continue
			}
			session.pacer.Update(frame.Data)
		}
	}()

	go pipeH264(encoder.Stdout(), videoTrack, a.instanceID)

	return session, nil
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
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterDefaultCodecs(); err != nil {
		return "", fmt.Errorf("register default codecs: %w", err)
	}
	interceptorRegistry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, interceptorRegistry); err != nil {
		return "", fmt.Errorf("register default interceptors: %w", err)
	}
	api := webrtc.NewAPI(
		webrtc.WithSettingEngine(se),
		webrtc.WithMediaEngine(mediaEngine),
		webrtc.WithInterceptorRegistry(interceptorRegistry),
	)

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

	// Start gRPC+ffmpeg video capture (see startVideoCapture's doc comment).
	// Deliberately no fallback to the retired adb-screenrecord path on
	// failure here — its measured ~1.9 fps is unacceptable for the product,
	// and silently degrading to it would hide a real operational problem
	// (gRPC unreachable, ffmpeg missing) behind a connection that appears to
	// succeed. The caller (clientapi/signaling.go -> transport.Service) and
	// the Flutter client's existing "Connection failed" + Retry UI already
	// handle this error path correctly.
	capture, err := a.startVideoCapture(pc, videoTrack)
	if err != nil {
		_ = pc.Close()
		log.Printf("capture: start failed for instance=%s: %v", a.instanceID, err)
		return "", err
	}

	a.mu.Lock()
	a.pc = pc
	a.capture = capture
	a.mu.Unlock()

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
		log.Printf("input: bad message %q: %v", string(msg.Data), err)
		return
	}
	if err := a.handleInputEvent(event); err != nil {
		log.Printf("input: forward %+v failed: %v", event, err)
	} else {
		log.Printf("input: forwarded %+v (serial=%s role=%s)", event, a.adbSerial, a.Role())
	}
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
// start codes (0x00 0x00 0x01 or 0x00 0x00 0x00 0x01), groups NALs into
// access units, and writes each access unit as one RTP sample.
//
// Grouping matters, it's not just tidiness: pion's TrackLocalStaticSample
// advances its RTP timestamp by Sample.Duration on every WriteSample call,
// so writing each NAL as its own sample gives SPS/PPS/SEI/slice — all part
// of the SAME video frame — different RTP timestamps. A receiver can never
// reassemble those into one decodable access unit; every frame fails to
// decode even though bytes clearly arrive. This went unnoticed with the
// previous adb-screenrecord capture source because Android's encoder
// rarely re-sent parameter sets per frame, so most frames really were a
// single NAL — ffmpeg-produced H264 (confirmed via h264_videotoolbox)
// emits an SEI NAL alongside SPS/PPS on every keyframe, hitting this
// constantly. pion's own H264 payloader (see emitNalus in
// pion/rtp/codecs/h264_packet.go) is explicitly built to accept a
// multi-NAL Annex-B buffer per Sample and split it correctly — that's the
// API this should have been using all along.
//
// Multi-slice encoders add a second boundary problem: one picture can be
// spread over many slice NALs (libx264 with -tune zerolatency emits one per
// CPU core), and all of them must share one timestamp. See addNAL's
// first_mb_in_slice grouping.
// sampleWriter is the one method pipeH264 needs from a video track —
// satisfied by *webrtc.TrackLocalStaticSample; a fake in tests captures
// WriteSample calls without a real PeerConnection.
type sampleWriter interface {
	WriteSample(sample media.Sample) error
}

func pipeH264(r io.Reader, track sampleWriter, tag string) {
	var buf []byte     // raw bytes read but not yet scanned for a start code
	var pending []byte // NALs (with start codes restored) for the access unit not yet emitted
	pendingHasSlice := false
	tmp := make([]byte, 4096)
	var totalBytes, totalNALs, totalAccessUnits int64

	emitAccessUnit := func() {
		if len(pending) == 0 {
			return
		}
		totalAccessUnits++
		if werr := track.WriteSample(media.Sample{
			Data:     pending,
			Duration: time.Second / 30,
		}); werr != nil && totalAccessUnits <= 5 {
			log.Printf("capture[%s]: WriteSample failed: %v", tag, werr)
		}
		pending = nil
		pendingHasSlice = false
	}

	// addNAL appends nal (header byte + payload, start code already
	// stripped by the caller) to the pending access unit, restoring a
	// start code so pion's payloader can split multi-NAL samples back
	// apart.
	//
	// A slice NAL (type 1 or 5) does NOT necessarily end the access unit.
	// ffmpeg's libx264 under -tune zerolatency emits one slice per CPU core
	// (16 slices per picture on this machine's 9700X), and every slice of
	// one picture must share one RTP timestamp — writing each slice as its
	// own sample made pion advance the timestamp 16× faster than the real
	// frame rate. Slice headers carry first_mb_in_slice; a slice starting at
	// macroblock 0 begins a new picture, so only that one flushes the
	// pending access unit. AUD NALs (type 9) are an explicit boundary when a
	// given encoder emits them; parameter sets (SPS/PPS/SEI) belong to the
	// picture that follows them, so they are kept in the pending buffer
	// rather than flushed on their own.
	addNAL := func(nal []byte) {
		if len(nal) == 0 {
			return
		}
		totalNALs++
		nalType := nal[0] & 0x1F
		appendNAL := func() {
			pending = append(pending, 0x00, 0x00, 0x00, 0x01)
			pending = append(pending, nal...)
		}

		switch nalType {
		case 9: // access unit delimiter
			if pendingHasSlice {
				emitAccessUnit()
			}
			appendNAL()
		case 1, 5: // slice (non-IDR / IDR)
			firstMB, ok := firstMBInSlice(nal)
			if ok && firstMB == 0 && pendingHasSlice {
				emitAccessUnit()
			}
			appendNAL()
			pendingHasSlice = true
			if !ok {
				// Unparseable slice header: fall back to the old
				// one-NAL-per-sample behavior instead of risking merging
				// two pictures into one timestamp.
				emitAccessUnit()
			}
		default: // SPS, PPS, SEI, ...
			if pendingHasSlice {
				emitAccessUnit()
			}
			appendNAL()
		}
	}

	for {
		n, err := r.Read(tmp)
		if n > 0 {
			totalBytes += int64(n)
			if totalBytes == int64(n) {
				log.Printf("capture[%s]: first read %d bytes", tag, n)
			}
			buf = append(buf, tmp[:n]...)
			// Scan for NAL unit boundaries
			for {
				idx := findStartCode(buf)
				if idx < 0 {
					break
				}
				if idx > 0 {
					addNAL(buf[:idx])
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
			addNAL(buf)
			emitAccessUnit() // flush a trailing NAL that never got a following slice
			log.Printf("capture[%s]: stream ended after %d bytes, %d NALs, %d access units: %v", tag, totalBytes, totalNALs, totalAccessUnits, err)
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

// firstMBInSlice decodes first_mb_in_slice (the first ue(v) field of an
// H.264 slice header, immediately after the one-byte NAL header). 0 means
// this slice starts a new picture; any other value is a continuation slice
// of the picture already being assembled. Returns ok=false when the NAL is
// too short or malformed, so callers can fall back conservatively.
func firstMBInSlice(nal []byte) (uint64, bool) {
	if len(nal) < 2 {
		return 0, false
	}
	bitPos := 8 // skip nal_unit_header (forbidden_zero_bit + nal_ref_idc + type)
	readBit := func() (uint64, bool) {
		if bitPos/8 >= len(nal) {
			return 0, false
		}
		b := (nal[bitPos/8] >> (7 - uint(bitPos%8))) & 1
		bitPos++
		return uint64(b), true
	}

	zeros := 0
	for {
		b, ok := readBit()
		if !ok {
			return 0, false
		}
		if b == 1 {
			break
		}
		zeros++
		if zeros > 31 {
			return 0, false
		}
	}

	var suffix uint64
	for i := 0; i < zeros; i++ {
		b, ok := readBit()
		if !ok {
			return 0, false
		}
		suffix = (suffix << 1) | b
	}
	return (1 << uint(zeros)) - 1 + suffix, true
}
