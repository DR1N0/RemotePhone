package webrtc

import (
	"fmt"
	"math"
	"os/exec"
	"strconv"

	"mako-worker/internal/transport"
)

// BuildADBInputArgs returns the adb shell input args for a normalized InputEvent.
// screenW and screenH are the device pixel dimensions.
func BuildADBInputArgs(adbSerial string, ev transport.InputEvent, screenW, screenH int) []string {
	xPx := strconv.Itoa(int(math.Round(ev.X * float64(screenW))))
	yPx := strconv.Itoa(int(math.Round(ev.Y * float64(screenH))))
	base := []string{"adb", "-s", adbSerial, "shell", "input"}
	switch ev.Type {
	case "key":
		return append(base, "keyevent", strconv.Itoa(ev.Keycode))
	default: // "touch"
		return append(base, "tap", xPx, yPx)
	}
}

// ForwardInput executes the ADB input command for the given event.
// Returns an error if the role is "observer" (input is silently blocked).
func ForwardInput(adbSerial string, ev transport.InputEvent, role string, screenW, screenH int) error {
	if role == "observer" {
		return fmt.Errorf("input blocked for observer role")
	}
	args := BuildADBInputArgs(adbSerial, ev, screenW, screenH)
	return exec.Command(args[0], args[1:]...).Run()
}
