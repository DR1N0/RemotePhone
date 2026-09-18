package webrtc

import (
	"fmt"
	"testing"
	"time"

	"github.com/pion/webrtc/v3"
	"mako-worker/internal/transport"
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
