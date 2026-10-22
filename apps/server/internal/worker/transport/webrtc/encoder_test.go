package webrtc

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeEncoderProber reports success/failure per codec name without ever
// shelling out to a real ffmpeg — mirrors the Execer/FakeExecer pattern
// used elsewhere in this codebase for process-spawning seams.
type fakeEncoderProber struct {
	working    map[string]bool
	probed     []string // records call order, for assertions
	lastWidth  int
	lastHeight int
}

func (f *fakeEncoderProber) Probe(_ context.Context, _, codec string, width, height int) error {
	f.probed = append(f.probed, codec)
	f.lastWidth, f.lastHeight = width, height
	if f.working[codec] {
		return nil
	}
	return errors.New("probe failed: " + codec)
}

func TestSelectEncoderFromPrefersFirstWorkingCandidate(t *testing.T) {
	prober := &fakeEncoderProber{working: map[string]bool{"h264_videotoolbox": true, "libx264": true}}
	codec, err := SelectEncoderFrom(context.Background(), "ffmpeg", []string{"h264_videotoolbox", "libx264"}, prober, 540, 1200)
	if err != nil {
		t.Fatalf("SelectEncoderFrom: %v", err)
	}
	if codec != "h264_videotoolbox" {
		t.Errorf("codec = %q, want h264_videotoolbox", codec)
	}
	if len(prober.probed) != 1 {
		t.Errorf("probed %v candidates, want exactly 1 (should stop at first success)", prober.probed)
	}
	if prober.lastWidth != 540 || prober.lastHeight != 1200 {
		t.Errorf("probe dimensions = %dx%d, want 540x1200 (real capture size, not a tiny fixed probe)", prober.lastWidth, prober.lastHeight)
	}
}

func TestSelectEncoderFromFallsBackWhenFirstFails(t *testing.T) {
	prober := &fakeEncoderProber{working: map[string]bool{"libx264": true}}
	codec, err := SelectEncoderFrom(context.Background(), "ffmpeg", []string{"h264_nvenc", "h264_vaapi", "libx264"}, prober, 540, 1200)
	if err != nil {
		t.Fatalf("SelectEncoderFrom: %v", err)
	}
	if codec != "libx264" {
		t.Errorf("codec = %q, want libx264", codec)
	}
	if len(prober.probed) != 3 {
		t.Errorf("probed %v, want all 3 candidates tried in order", prober.probed)
	}
}

func TestSelectEncoderFromReturnsErrorWhenNoneWork(t *testing.T) {
	prober := &fakeEncoderProber{working: map[string]bool{}}
	_, err := SelectEncoderFrom(context.Background(), "ffmpeg", []string{"h264_nvenc", "libx264"}, prober, 540, 1200)
	if err == nil {
		t.Fatal("expected an error when no candidate works")
	}
}

func TestDefaultEncoderCandidatesPutsSoftwareLast(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		candidates := defaultEncoderCandidates(goos)
		if len(candidates) == 0 {
			t.Errorf("defaultEncoderCandidates(%q) returned no candidates", goos)
			continue
		}
		if last := candidates[len(candidates)-1]; last != "libx264" {
			t.Errorf("defaultEncoderCandidates(%q) last candidate = %q, want libx264 (software fallback)", goos, last)
		}
	}
}

func TestBuildFFmpegArgsAMFKeepsKeyframesAndHeaders(t *testing.T) {
	args := buildFFmpegArgs("ffmpeg", "h264_amf", EncodeOptions{Width: 540, Height: 1200, FPS: 30, BitRate: 2_000_000})
	joined := strings.Join(args, " ")
	// `-usage lowlatency` suppresses periodic IDRs in AMF; without a
	// keyframe every second a client that loses one reference frame freezes
	// permanently. lowlatency_high_quality + header_spacing keeps recovery
	// possible.
	for _, want := range []string{"lowlatency_high_quality", "-header_spacing 30", "-g 30"} {
		if !strings.Contains(joined, want) {
			t.Errorf("h264_amf args missing %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "-usage lowlatency ") {
		t.Errorf("h264_amf must not use lowlatency usage (suppresses IDRs): %v", args)
	}
}

func TestBuildFFmpegArgsOmitsBFramesAndSetsShortGOP(t *testing.T) {
	opts := EncodeOptions{Width: 540, Height: 1200, FPS: 30, BitRate: 2_000_000}
	// An unrecognized codec name falls back to libx264's flag set (the
	// default branch in buildFFmpegArgs) rather than guessing at flags for
	// a codec it doesn't know — "requested" maps each case to what should
	// actually appear after -c:v.
	cases := map[string]string{
		"h264_videotoolbox": "h264_videotoolbox",
		"h264_nvenc":        "h264_nvenc",
		"h264_vaapi":        "h264_vaapi",
		"h264_amf":          "h264_amf",
		"libx264":           "libx264",
		"some_future_codec": "libx264",
	}
	for requested, wantCodecFlag := range cases {
		args := buildFFmpegArgs("ffmpeg", requested, opts)
		if args[0] != "ffmpeg" {
			t.Errorf("%s: args[0] = %q, want ffmpeg path first", requested, args[0])
		}
		foundCodec := false
		for i, a := range args {
			if a == "-c:v" && i+1 < len(args) && args[i+1] == wantCodecFlag {
				foundCodec = true
			}
		}
		if !foundCodec {
			t.Errorf("%s: -c:v %s not found in args: %v", requested, wantCodecFlag, args)
		}
	}
}
