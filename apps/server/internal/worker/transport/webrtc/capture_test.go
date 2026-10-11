package webrtc_test

import (
	"testing"

	"mako/internal/worker/transport/webrtc"
)

func TestCaptureBuildsCorrectADBArgs(t *testing.T) {
	args := webrtc.BuildScreenrecordArgs("localhost:5554")
	if len(args) < 5 {
		t.Fatalf("expected at least 5 args, got %d: %v", len(args), args)
	}
	if args[0] != "adb" {
		t.Fatalf("expected adb as first arg, got %q", args[0])
	}
	if args[1] != "-s" || args[2] != "localhost:5554" {
		t.Fatalf("expected -s localhost:5554, got %v", args[1:3])
	}
	found := false
	for _, a := range args {
		if a == "--output-format=h264" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected --output-format=h264 flag in args")
	}
}
