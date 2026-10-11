package webrtc

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"mako/internal/worker/transport"
)

func TestAdapterUsesItsCurrentRoleForInput(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "observer", "adb", 1080, 1920, "")
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
	adapter := NewAdapter("instance-1", "attachment-1", "observer", "adb", 1080, 1920, "")

	err := adapter.HandleInput(transport.InputEvent{Type: "touch"})

	if err == nil {
		t.Fatal("expected observer input to be rejected")
	}
}

func TestHandleDataChannelOnlyBindsInputLabel(t *testing.T) {
	adapter := NewAdapter("instance-1", "attachment-1", "participant", "adb", 1080, 1920, "")
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

	adapter := NewAdapter("instance-1", "attachment-1", "participant", "adb", 1080, 1920, "")
	adapter.startCapture = func(string) (*ScreenCapture, io.ReadCloser, error) {
		return &ScreenCapture{}, io.NopCloser(strings.NewReader("")), nil
	}
	defer func() { _ = adapter.Stop() }()

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
