package webrtc

import (
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"strconv"

	"mako/internal/worker/transport"
)

var wmSizeRE = regexp.MustCompile(`(\d+)x(\d+)`)

// QueryScreenSize returns adbSerial's actual display resolution via
// "adb shell wm size", falling back to (fallbackW, fallbackH) if the device
// is unreachable or the output doesn't parse — AVD profiles vary (e.g.
// Medium_Phone_API_36.1 is 1080x2400, not the older 1080x1920 default),
// so hardcoding one resolution silently mis-scales every touch event.
func QueryScreenSize(adbSerial string, fallbackW, fallbackH int) (int, int) {
	out, err := exec.Command("adb", "-s", adbSerial, "shell", "wm", "size").Output()
	if err != nil {
		return fallbackW, fallbackH
	}
	m := wmSizeRE.FindStringSubmatch(string(out))
	if m == nil {
		return fallbackW, fallbackH
	}
	w, errW := strconv.Atoi(m[1])
	h, errH := strconv.Atoi(m[2])
	if errW != nil || errH != nil || w <= 0 || h <= 0 {
		return fallbackW, fallbackH
	}
	return w, h
}

// BuildADBInputArgs returns the adb shell input args for a normalized InputEvent.
// screenW and screenH are the device pixel dimensions. Touch down/move/up map
// to "input motionevent DOWN/MOVE/UP" (not "input tap", which can only ever
// express an instantaneous tap-at-a-point and can't express a drag) so a
// down+move*+up sequence from the client becomes one continuous gesture on
// the device — a drag if the position changed between down and up, a tap if
// it didn't.
func BuildADBInputArgs(adbSerial string, ev transport.InputEvent, screenW, screenH int) []string {
	xPx := strconv.Itoa(int(math.Round(ev.X * float64(screenW))))
	yPx := strconv.Itoa(int(math.Round(ev.Y * float64(screenH))))
	base := []string{"adb", "-s", adbSerial, "shell", "input"}
	switch ev.Type {
	case "key":
		return append(base, "keyevent", strconv.Itoa(ev.Keycode))
	default: // "touch"
		switch ev.Action {
		case "down":
			return append(base, "motionevent", "DOWN", xPx, yPx)
		case "move":
			return append(base, "motionevent", "MOVE", xPx, yPx)
		default: // "up"
			return append(base, "motionevent", "UP", xPx, yPx)
		}
	}
}

// ForwardInput executes the ADB input command for the given event.
// Only owner and participant roles may forward input.
func ForwardInput(adbSerial string, ev transport.InputEvent, role string, screenW, screenH int) error {
	if !allowsInput(role) {
		return fmt.Errorf("input blocked for %q role", role)
	}
	args := BuildADBInputArgs(adbSerial, ev, screenW, screenH)
	return exec.Command(args[0], args[1:]...).Run()
}

func allowsInput(role string) bool {
	return role == "owner" || role == "participant"
}
