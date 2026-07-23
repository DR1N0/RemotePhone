package webrtc_test

import (
	"testing"

	"mako-worker/internal/transport"
	webrtctransport "mako-worker/internal/transport/webrtc"
)

func TestBuildADBInputArgs_Touch(t *testing.T) {
	ev := transport.InputEvent{Type: "touch", Action: "down", X: 0.5, Y: 0.25}
	args := webrtctransport.BuildADBInputArgs("localhost:5554", ev, 1080, 1920)
	// expect: adb -s localhost:5554 shell input tap 540 480
	if args[0] != "adb" || args[1] != "-s" || args[2] != "localhost:5554" {
		t.Fatalf("unexpected prefix: %v", args[:3])
	}
	if args[5] != "tap" {
		t.Fatalf("expected 'tap' subcommand, got %q", args[5])
	}
	if args[6] != "540" {
		t.Fatalf("expected x=540 (0.5 * 1080), got %q", args[6])
	}
	if args[7] != "480" {
		t.Fatalf("expected y=480 (0.25 * 1920), got %q", args[7])
	}
}

func TestBuildADBInputArgs_Key(t *testing.T) {
	ev := transport.InputEvent{Type: "key", Keycode: 4}
	args := webrtctransport.BuildADBInputArgs("localhost:5554", ev, 1080, 1920)
	if args[5] != "keyevent" {
		t.Fatalf("expected 'keyevent' subcommand, got %q", args[5])
	}
	if args[6] != "4" {
		t.Fatalf("expected keycode 4, got %q", args[6])
	}
}

func TestObserverInputIsRejected(t *testing.T) {
	err := webrtctransport.ForwardInput("localhost:5554", transport.InputEvent{Type: "touch"}, "observer", 1080, 1920)
	if err == nil {
		t.Fatal("expected error for observer role, got nil")
	}
}
