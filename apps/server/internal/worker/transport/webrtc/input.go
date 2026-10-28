package webrtc

import (
	"context"
	"fmt"
	"log"
	"math"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"remotephone/internal/worker/transport"
)

var wmSizeRE = regexp.MustCompile(`(\d+)x(\d+)`)
var displayRotationRE = regexp.MustCompile(`mCurrentRotation=ROTATION_(\d+)`)
var displayViewportOrientationRE = regexp.MustCompile(`displayId=0[^\]]*orientation=(\d+)`)

// ConfigureGuestNavigation switches the emulated Android device from
// edge-swipe gesture navigation to three-button navigation. The phone's own
// back/home gestures cannot be forwarded to the guest (the phone OS consumes
// them), and edge swipes inside the video would otherwise trigger the guest's
// back gesture as well. Stream clients get explicit Back/Home/Recents buttons
// instead. Best-effort: an old image where the overlay name is missing just
// logs and keeps the existing navigation mode.
func ConfigureGuestNavigation(adbSerial string) {
	if adbSerial == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "adb", "-s", adbSerial, "shell",
		"cmd", "overlay", "enable-exclusive", "--category",
		"com.android.internal.systemui.navbar.threebutton").CombinedOutput()
	if err != nil {
		log.Printf("guest nav: switch %s to three-button failed: %v (%s)",
			adbSerial, err, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("guest nav: %s uses three-button navigation", adbSerial)
}

// QueryScreenSize returns adbSerial's actual display resolution via
// "adb shell wm size", falling back to (fallbackW, fallbackH) if the device
// is unreachable or the output doesn't parse — AVD profiles vary (e.g.
// Medium_Phone_API_36.1 is 1080x2400, not the older 1080x1920 default),
// so hardcoding one resolution silently mis-scales every touch event.
func QueryScreenSize(adbSerial string, fallbackW, fallbackH int) (int, int) {
	// The worker can ask this immediately after spawning the emulator, before
	// adbd is ready. Retry briefly instead of permanently falling back to a
	// stale profile resolution and mis-scaling touch input.
	for attempt := 0; attempt < 5; attempt++ {
		out, err := exec.Command("adb", "-s", adbSerial, "shell", "wm", "size").Output()
		if err == nil {
			m := wmSizeRE.FindStringSubmatch(string(out))
			if m != nil {
				w, errW := strconv.Atoi(m[1])
				h, errH := strconv.Atoi(m[2])
				if errW == nil && errH == nil && w > 0 && h > 0 {
					return w, h
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fallbackW, fallbackH
}

// QueryDisplayRotation returns the current display rotation in degrees
// (0/90/180/270). The emulator's gRPC screenshot stream can return frames in
// the panel's natural orientation while Android composes a rotated image, so
// the capture pipeline needs this to normalize the frame.
func QueryDisplayRotation(adbSerial string) int {
	// Prefer the compact display dump's viewport orientation; dumpsys window
	// is large and its current-rotation field is not present in every vendor
	// build. Both report the same 0/1/2/3 rotation.
	if out, err := exec.Command("adb", "-s", adbSerial, "shell", "dumpsys", "display").Output(); err == nil {
		if m := displayViewportOrientationRE.FindStringSubmatch(string(out)); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return n * 90
			}
		}
	}
	out, err := exec.Command("adb", "-s", adbSerial, "shell", "dumpsys", "window").Output()
	if err != nil {
		return 0
	}
	m := displayRotationRE.FindStringSubmatch(string(out))
	if m == nil {
		return 0
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return 0
	}
	return n * 90
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
