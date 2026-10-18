package webrtc

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"
)

// ResolveFFmpegBinary finds the ffmpeg executable: an explicit override
// first, then $PATH, then common install locations — mirrors
// runtime/androidstudio/discover.go's ResolveEmulatorBinary candidate-list
// pattern.
func ResolveFFmpegBinary(explicitPath string) (string, error) {
	candidates := make([]string, 0, 4)
	if explicitPath != "" {
		candidates = append(candidates, explicitPath)
	}
	if found, err := exec.LookPath("ffmpeg"); err == nil {
		candidates = append(candidates, found)
	}
	candidates = append(candidates,
		"/opt/homebrew/bin/ffmpeg", // macOS, Apple Silicon Homebrew
		"/usr/local/bin/ffmpeg",    // macOS, Intel Homebrew
		"/usr/bin/ffmpeg",          // Linux distro packages
	)

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("ffmpeg binary not found (checked explicit path, $PATH, and common install locations)")
}

// defaultEncoderCandidates returns platform-ordered H264 encoder names to
// try, hardware first: real encode hardware is an order of magnitude lower
// latency and CPU cost than software x264, which matters at 30-60fps. The
// software fallback is listed last on every platform so capture always has
// a working option.
func defaultEncoderCandidates(goos string) []string {
	switch goos {
	case "darwin":
		return []string{"h264_videotoolbox", "libx264"}
	case "linux":
		return []string{"h264_nvenc", "h264_vaapi", "libx264"}
	case "windows":
		// h264_amf wraps AMD's Advanced Media Framework — the Windows
		// equivalent of nvenc for AMD GPUs (vaapi doesn't exist on
		// Windows at all). nvenc stays first since it also works on
		// Windows for NVIDIA GPUs.
		return []string{"h264_nvenc", "h264_amf", "libx264"}
	default:
		return []string{"libx264"}
	}
}

// EncoderProber checks whether codec actually initializes on this machine —
// compiled-in ffmpeg support isn't the same as "will work" (VAAPI needs a
// real /dev/dri render node, NVENC needs a loaded NVIDIA driver, etc).
type EncoderProber interface {
	Probe(ctx context.Context, ffmpegPath, codec string) error
}

// RealEncoderProber runs a tiny throwaway encode (`-f null -`, the standard
// ffmpeg way to exercise a codec without producing real output) under a
// caller-supplied timeout. A non-zero exit means the codec didn't actually
// initialize on this machine.
type RealEncoderProber struct{}

func (RealEncoderProber) Probe(ctx context.Context, ffmpegPath, codec string) error {
	cmd := exec.CommandContext(ctx, ffmpegPath,
		"-f", "lavfi", "-i", "color=black:s=64x64:d=0.2",
		"-c:v", codec,
		"-f", "null", "-",
	)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("probe %s: %w", codec, err)
	}
	return nil
}

const probeTimeout = 2 * time.Second

// SelectEncoder tries each of defaultEncoderCandidates(runtime.GOOS) in
// order via RealEncoderProber, returning the first one that actually
// initializes. Call once per worker process (e.g. memoized by the caller)
// — probing is real subprocess work, not free, and candidate availability
// doesn't change between connections on the same host.
func SelectEncoder(ctx context.Context, ffmpegPath string) (string, error) {
	return SelectEncoderFrom(ctx, ffmpegPath, defaultEncoderCandidates(runtime.GOOS), RealEncoderProber{})
}

// SelectEncoderFrom is SelectEncoder's testable core: explicit candidates
// and prober, so tests never shell out to a real ffmpeg.
func SelectEncoderFrom(ctx context.Context, ffmpegPath string, candidates []string, prober EncoderProber) (string, error) {
	var errs []error
	for _, codec := range candidates {
		probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		err := prober.Probe(probeCtx, ffmpegPath, codec)
		cancel()
		if err == nil {
			return codec, nil
		}
		errs = append(errs, err)
	}
	return "", fmt.Errorf("no working H264 encoder found (tried %d candidates): %w", len(candidates), joinErrs(errs))
}

var (
	encoderOnce       sync.Once
	resolvedFFmpegBin string
	encoderChoice     string
	encoderErr        error
)

// cachedSelectEncoder resolves the ffmpeg binary and selects a working H264
// encoder exactly once per worker process, returning the resolved ffmpeg
// path alongside the chosen codec — neither changes between connections on
// the same host, and both are real subprocess work, not free. Every
// HandleOffer call shares this one cached result (or error) for the
// process's lifetime.
func cachedSelectEncoder(ctx context.Context, explicitFFmpegPath string) (ffmpegPath, codec string, err error) {
	encoderOnce.Do(func() {
		resolvedFFmpegBin, encoderErr = ResolveFFmpegBinary(explicitFFmpegPath)
		if encoderErr != nil {
			return
		}
		encoderChoice, encoderErr = SelectEncoder(ctx, resolvedFFmpegBin)
	})
	return resolvedFFmpegBin, encoderChoice, encoderErr
}

func joinErrs(errs []error) error {
	if len(errs) == 0 {
		return nil
	}
	msg := errs[0].Error()
	for _, e := range errs[1:] {
		msg += "; " + e.Error()
	}
	return fmt.Errorf("%s", msg)
}

// EncodeOptions describes the raw input stream an Encoder will consume.
type EncodeOptions struct {
	Width, Height int
	FPS           int
	BitRate       int // bits per second
}

// h264Level matches the level-id ("1f" = 31 = level 3.1) common to every
// H264 profile-level-id pion/webrtc registers by default (see
// mediaengine.go: 42001f, 42e01f, 4d001f, 64001f all share it) — keeping
// our encode's level consistent with whatever profile gets negotiated.
const h264Level = "3.1"

// buildFFmpegArgs constructs the ffmpeg invocation for codec, reading
// headerless raw RGBA8888 frames from stdin at a fixed frame rate (the
// pacer in pacer.go is what makes that rate actually constant) and writing
// an H264 Annex-B elementary stream to stdout. Flags are tuned for latency
// over quality: no B-frames, a short (1-second) GOP so new viewers get a
// keyframe quickly, and each encoder's own low-latency/realtime mode.
//
// Every branch pins -profile:v baseline explicitly: hardware encoders
// default to whatever profile they think gives the best quality (measured
// h264_videotoolbox defaults to High, which uses CABAC entropy coding),
// but WebRTC's negotiated codec can land on a Baseline profile-level-id
// (CAVLC only, no CABAC) — a mismatch there isn't just a quality
// difference, it silently produces an undecodable stream: the connection
// stays "Connected" with RTP flowing, but nothing ever renders. Baseline
// is the safe, universally-decodable choice; revisit only if negotiating
// a higher profile end-to-end is deliberately desired later.
//
// nvenc/vaapi/amf flag specifics are necessarily best-effort here — this
// development environment has no Linux or AMD GPU to verify them against;
// expect to tune on first real Linux/Windows deployment.
func buildFFmpegArgs(ffmpegPath, codec string, opts EncodeOptions) []string {
	size := fmt.Sprintf("%dx%d", opts.Width, opts.Height)
	args := []string{
		ffmpegPath,
		"-f", "rawvideo", "-pix_fmt", "rgba", "-s", size, "-framerate", strconv.Itoa(opts.FPS),
		"-i", "pipe:0",
	}

	switch codec {
	case "h264_videotoolbox":
		args = append(args,
			"-c:v", "h264_videotoolbox", "-pix_fmt", "yuv420p", "-realtime", "true", "-allow_sw", "0",
			"-profile:v", "baseline", "-level:v", h264Level,
			"-b:v", strconv.Itoa(opts.BitRate), "-g", strconv.Itoa(opts.FPS),
		)
	case "h264_nvenc":
		args = append(args,
			"-c:v", "h264_nvenc", "-pix_fmt", "yuv420p", "-preset", "p1", "-tune", "ll", "-rc", "cbr",
			"-profile:v", "baseline", "-level:v", h264Level,
			"-b:v", strconv.Itoa(opts.BitRate), "-g", strconv.Itoa(opts.FPS), "-bf", "0",
		)
	case "h264_vaapi":
		// Pixel format conversion happens via the hwupload filter chain
		// below (vaapi encodes from an uploaded hardware surface, not a
		// plain -pix_fmt output flag) — nv12 is vaapi's expected format.
		args = append(args,
			"-vaapi_device", "/dev/dri/renderD128",
			"-vf", "format=nv12,hwupload",
			"-c:v", "h264_vaapi",
			"-profile:v", "constrained_baseline", "-level:v", h264Level,
			"-b:v", strconv.Itoa(opts.BitRate), "-g", strconv.Itoa(opts.FPS), "-bf", "0",
		)
	case "h264_amf":
		args = append(args,
			"-c:v", "h264_amf", "-pix_fmt", "yuv420p", "-usage", "lowlatency", "-quality", "speed", "-rc", "cbr",
			"-profile:v", "constrained_baseline", "-level:v", h264Level,
			"-b:v", strconv.Itoa(opts.BitRate), "-g", strconv.Itoa(opts.FPS), "-bf", "0",
		)
	default: // libx264 and any unrecognized future candidate
		// -pix_fmt yuv420p is load-bearing, not cosmetic: libx264's default
		// chroma subsampling for a raw RGBA input is 4:4:4, which the
		// baseline profile above cannot represent — baseline requires 4:2:0
		// — and libx264 fails outright (zero bytes of output) rather than
		// downsampling for you.
		args = append(args,
			"-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "ultrafast", "-tune", "zerolatency",
			"-profile:v", "baseline", "-level:v", h264Level,
			"-b:v", strconv.Itoa(opts.BitRate), "-g", strconv.Itoa(opts.FPS), "-bf", "0",
		)
	}

	return append(args, "-f", "h264", "pipe:1")
}

// encoderProcess is the surface Adapter.startVideoCapture needs from an
// encoder — satisfied by *Encoder; a fake in tests avoids ever spawning a
// real ffmpeg subprocess.
type encoderProcess interface {
	Stdout() io.ReadCloser
	Write(frame []byte) (int, error)
	Stop()
}

// Encoder wraps a running ffmpeg subprocess: Write feeds it one raw RGBA
// frame, Stdout yields its H264 elementary-stream output (fed to the
// existing pipeH264 NAL parser, unchanged), Stop tears it down.
type Encoder struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
}

// StartEncoder launches ffmpeg configured for codec with the given options.
func StartEncoder(ffmpegPath, codec string, opts EncodeOptions) (*Encoder, error) {
	args := buildFFmpegArgs(ffmpegPath, codec, opts)
	cmd := exec.Command(args[0], args[1:]...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	return &Encoder{cmd: cmd, stdin: stdin, stdout: stdout}, nil
}

func (e *Encoder) Stdout() io.ReadCloser { return e.stdout }

// Write feeds one raw frame to ffmpeg's stdin, matching io.Writer so the
// pacer can use it directly.
func (e *Encoder) Write(frame []byte) (int, error) {
	return e.stdin.Write(frame)
}

// Stop kills the ffmpeg subprocess and waits for it to exit.
func (e *Encoder) Stop() {
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_ = e.cmd.Wait()
	}
}
