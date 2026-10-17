package webrtc

import (
	"testing"

	"mako/internal/worker/transport"
)

func TestBuildADBInputArgs_Touch(t *testing.T) {
	for _, tc := range []struct {
		action   string
		wantVerb string
	}{
		{"down", "DOWN"},
		{"move", "MOVE"},
		{"up", "UP"},
	} {
		ev := transport.InputEvent{Type: "touch", Action: tc.action, X: 0.5, Y: 0.25}
		args := BuildADBInputArgs("localhost:5554", ev, 1080, 1920)
		// expect: adb -s localhost:5554 shell input motionevent <VERB> 540 480
		if args[0] != "adb" || args[1] != "-s" || args[2] != "localhost:5554" {
			t.Fatalf("unexpected prefix: %v", args[:3])
		}
		if args[5] != "motionevent" {
			t.Fatalf("expected 'motionevent' subcommand, got %q", args[5])
		}
		if args[6] != tc.wantVerb {
			t.Fatalf("action %q: expected verb %q, got %q", tc.action, tc.wantVerb, args[6])
		}
		if args[7] != "540" {
			t.Fatalf("expected x=540 (0.5 * 1080), got %q", args[7])
		}
		if args[8] != "480" {
			t.Fatalf("expected y=480 (0.25 * 1920), got %q", args[8])
		}
	}
}

func TestBuildADBInputArgs_Key(t *testing.T) {
	ev := transport.InputEvent{Type: "key", Keycode: 4}
	args := BuildADBInputArgs("localhost:5554", ev, 1080, 1920)
	if args[5] != "keyevent" {
		t.Fatalf("expected 'keyevent' subcommand, got %q", args[5])
	}
	if args[6] != "4" {
		t.Fatalf("expected keycode 4, got %q", args[6])
	}
}

func TestObserverInputIsRejected(t *testing.T) {
	err := ForwardInput("localhost:5554", transport.InputEvent{Type: "touch"}, "observer", 1080, 1920)
	if err == nil {
		t.Fatal("expected observer input to be rejected")
	}
}

func TestUnknownInputRoleIsRejected(t *testing.T) {
	if allowsInput("administrator") {
		t.Fatal("expected input to be rejected for unknown role")
	}
}
