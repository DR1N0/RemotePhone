package webrtc

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"remotephone/internal/worker/transport"
	"remotephone/internal/worker/transport/emugrpc"
)

// fakeEncoderProcess satisfies encoderProcess without spawning a real
// ffmpeg subprocess — used by tests that exercise HandleOffer's full
// negotiation path but don't care about actual encoded video content.
type fakeEncoderProcess struct {
	stdout io.ReadCloser
}

func (f *fakeEncoderProcess) Stdout() io.ReadCloser           { return f.stdout }
func (f *fakeEncoderProcess) Write(frame []byte) (int, error) { return len(frame), nil }
func (f *fakeEncoderProcess) Stop()                           {}

func TestTouchPixelCoordinatesRotatesLandscapeInputIntoNaturalSpace(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "owner", "adb", 2400, 1080, "", "", "")
	adapter.rotation = 90
	x, y := adapter.touchPixelCoordinates(0.5, 0.5)
	if x != 540 || y != 1200 {
		t.Fatalf("rotated touch = (%d,%d), want (540,1200)", x, y)
	}
	adapter.rotation = 0
	x, y = adapter.touchPixelCoordinates(0.5, 0.5)
	if x != 1200 || y != 540 {
		t.Fatalf("unrotated touch = (%d,%d), want (1200,540)", x, y)
	}
}

func TestAdapterUsesItsCurrentRoleForInput(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "observer", "adb", 1080, 1920, "", "", "")
	var receivedRole string
	adapter.forwardInput = func(_ string, _ transport.InputEvent, role string, _, _ int) error {
		receivedRole = role
		return nil
	}

	if err := adapter.handleInputEvent(transport.InputEvent{Type: "touch"}); err != nil {
		t.Fatalf("handle observer input: %v", err)
	}
	if receivedRole != "observer" {
		t.Fatalf("got role %q, want observer", receivedRole)
	}

	adapter.SetRole("participant")
	if err := adapter.handleInputEvent(transport.InputEvent{Type: "touch"}); err != nil {
		t.Fatalf("handle participant input: %v", err)
	}
	if receivedRole != "participant" {
		t.Fatalf("got role %q, want participant", receivedRole)
	}
}

func TestObserverAdapterRejectsInput(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "observer", "adb", 1080, 1920, "", "", "")

	err := adapter.HandleInput(transport.InputEvent{Type: "touch"})

	if err == nil {
		t.Fatal("expected observer input to be rejected")
	}
}

func TestHandleDataChannelOnlyBindsInputLabel(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "participant", "adb", 1080, 1920, "", "", "")
	received := make(chan transport.InputEvent, 2)
	adapter.forwardInput = func(_ string, event transport.InputEvent, _ string, _, _ int) error {
		received <- event
		return nil
	}

	server, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create server peer connection: %v", err)
	}
	defer func() { _ = server.Close() }()
	server.OnDataChannel(adapter.handleDataChannel)

	client, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create client peer connection: %v", err)
	}
	defer func() { _ = client.Close() }()

	ignored, err := client.CreateDataChannel("ignored", nil)
	if err != nil {
		t.Fatalf("create ignored data channel: %v", err)
	}
	input, err := client.CreateDataChannel("input", nil)
	if err != nil {
		t.Fatalf("create input data channel: %v", err)
	}
	ignored.OnOpen(func() {
		if err := ignored.SendText(`{"type":"key","keycode":4}`); err != nil {
			t.Errorf("send ignored message: %v", err)
		}
	})
	input.OnOpen(func() {
		if err := input.SendText(`{"type":"touch","x":0.5,"y":0.25}`); err != nil {
			t.Errorf("send input message: %v", err)
		}
	})

	connectPeerConnections(t, client, server)

	select {
	case event := <-received:
		if event.Type != "touch" {
			t.Fatalf("got event type %q, want touch from input channel", event.Type)
		}
	case <-time.After(time.Second):
		t.Fatal("expected input channel message to be handled")
	}
	select {
	case event := <-received:
		t.Fatalf("unexpected event from non-input channel: %#v", event)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestHandleOfferNegotiatesH264VideoForRecvonlyOffer(t *testing.T) {
	offerer, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("create offerer: %v", err)
	}
	defer func() { _ = offerer.Close() }()

	if _, err := offerer.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("add video receiver: %v", err)
	}
	offer, err := offerer.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	offerGathering := webrtc.GatheringCompletePromise(offerer)
	if err := offerer.SetLocalDescription(offer); err != nil {
		t.Fatalf("set offerer local description: %v", err)
	}
	waitForTestGathering(t, offerGathering)

	adapter := NewAdapter("instance-1", "attachment-1", "participant", "adb", 1080, 1920, "", "", "")
	frameSource := emugrpc.NewFakeFrameSource()
	adapter.newFrameSource = func(string, int, int) emugrpc.FrameSource {
		return frameSource
	}
	adapter.selectEncoder = func(context.Context, string, int, int) (string, string, error) {
		return "ffmpeg", "libx264", nil
	}
	adapter.startEncoder = func(string, string, EncodeOptions) (encoderProcess, error) {
		return &fakeEncoderProcess{stdout: io.NopCloser(strings.NewReader(""))}, nil
	}
	defer func() { _ = adapter.Stop() }()

	// The emulator may return a landscape frame even though the AVD metadata
	// is portrait; startVideoCapture must size the encoder from this frame.
	frameSource.Push(emugrpc.Frame{Width: 960, Height: 432, Data: make([]byte, 960*432*4)})

	answer, err := adapter.HandleOffer("instance-1", "attachment-1", offerer.LocalDescription().SDP)
	if err != nil {
		t.Fatalf("handle offer: %v", err)
	}
	if !strings.Contains(answer, "m=video") {
		t.Fatalf("answer missing video media section:\n%s", answer)
	}
	if !strings.Contains(answer, "a=rtpmap") || !strings.Contains(strings.ToLower(answer), "h264") {
		t.Fatalf("answer missing H264 codec mapping:\n%s", answer)
	}
}

func TestWaitForGatheringReturnsTimeoutAfterSuppliedDeadline(t *testing.T) {
	deadline := 10 * time.Millisecond
	started := time.Now()
	err := waitForGathering(make(chan struct{}), deadline)
	if err == nil || err.Error() != "gather ICE candidates: timeout" {
		t.Fatalf("got error %v, want gathering timeout", err)
	}
	if elapsed := time.Since(started); elapsed < deadline {
		t.Fatalf("returned after %s, before supplied deadline %s", elapsed, deadline)
	}
}

func connectPeerConnections(t *testing.T, client, server *webrtc.PeerConnection) {
	t.Helper()
	offer, err := client.CreateOffer(nil)
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	clientGathering := webrtc.GatheringCompletePromise(client)
	if err := client.SetLocalDescription(offer); err != nil {
		t.Fatalf("set client local description: %v", err)
	}
	waitForTestGathering(t, clientGathering)
	if err := server.SetRemoteDescription(*client.LocalDescription()); err != nil {
		t.Fatalf("set server remote description: %v", err)
	}

	answer, err := server.CreateAnswer(nil)
	if err != nil {
		t.Fatalf("create answer: %v", err)
	}
	serverGathering := webrtc.GatheringCompletePromise(server)
	if err := server.SetLocalDescription(answer); err != nil {
		t.Fatalf("set server local description: %v", err)
	}
	waitForTestGathering(t, serverGathering)
	if err := client.SetRemoteDescription(*server.LocalDescription()); err != nil {
		t.Fatalf("set client remote description: %v", err)
	}
}

func waitForTestGathering(t *testing.T, gathering <-chan struct{}) {
	t.Helper()
	select {
	case <-gathering:
	case <-time.After(time.Second):
		t.Fatal(fmt.Errorf("timed out waiting for ICE gathering"))
	}
}

type fakeTouchSender struct {
	points []emugrpc.TouchPoint
	err    error
}

func (f *fakeTouchSender) SendTouch(_ context.Context, points []emugrpc.TouchPoint) error {
	if f.err != nil {
		return f.err
	}
	f.points = append(f.points, points...)
	return nil
}

func (f *fakeTouchSender) Close() error { return nil }

func TestHandleInputSendsTouchThroughGRPCWithPointerID(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "participant", "adb", 1080, 2400, "", "127.0.0.1:8554", "")
	sender := &fakeTouchSender{}
	adapter.touchSender = sender
	adapter.forwardInput = func(string, transport.InputEvent, string, int, int) error {
		t.Fatal("touch should not fall back to adb when gRPC succeeds")
		return nil
	}

	if err := adapter.HandleInput(transport.InputEvent{Type: "touch", Action: "down", X: 0.5, Y: 0.25, PointerID: 3}); err != nil {
		t.Fatalf("HandleInput down: %v", err)
	}
	if err := adapter.HandleInput(transport.InputEvent{Type: "touch", Action: "up", X: 0.5, Y: 0.25, PointerID: 3}); err != nil {
		t.Fatalf("HandleInput up: %v", err)
	}

	if len(sender.points) != 2 {
		t.Fatalf("sent %d points, want 2", len(sender.points))
	}
	down := sender.points[0]
	if down.ID != 3 || down.X != 540 || down.Y != 600 || down.Pressure != 1 {
		t.Errorf("down point = %+v, want id=3 x=540 y=600 pressure=1", down)
	}
	up := sender.points[1]
	if up.ID != 3 || up.Pressure != 0 {
		t.Errorf("up point = %+v, want id=3 pressure=0", up)
	}
}

func TestHandleInputFallsBackToADBWhenTouchRPCFails(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "participant", "adb", 1080, 2400, "", "127.0.0.1:8554", "")
	adapter.touchSender = &fakeTouchSender{err: fmt.Errorf("emulator has no sendTouch")}
	var forwarded bool
	adapter.forwardInput = func(_ string, _ transport.InputEvent, role string, _, _ int) error {
		forwarded = true
		if role != "participant" {
			t.Errorf("fallback role = %q, want participant", role)
		}
		return nil
	}

	if err := adapter.HandleInput(transport.InputEvent{Type: "touch", Action: "down", X: 0.1, Y: 0.1}); err != nil {
		t.Fatalf("HandleInput: %v", err)
	}
	if !forwarded {
		t.Error("expected adb fallback after sendTouch failure")
	}
}
