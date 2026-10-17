// Package emugrpc is mako's client for the Android Emulator's own gRPC
// EmulatorController service — specifically the streamScreenshot RPC, which
// is mako's video-capture frame source (replacing `adb exec-out
// screenrecord`, which measured ~1.9 fps under active interaction — see
// docs/plans' "gRPC + hardware-encoded video capture" for the full
// rationale). The vendored, hand-trimmed service definition lives in
// proto/emulator_controller.proto.
//
// FrameSource is deliberately narrow and fake-able in tests, mirroring
// internal/controlplane/workerproc's Execer/FakeExecer pattern: GRPCFrameSource
// is the real implementation; FakeFrameSource (fake.go) is the test double.
package emugrpc

import (
	"context"
	"fmt"
	"io"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	emupb "mako/internal/worker/transport/emugrpc/proto"
)

// Frame is one raw RGBA8888 video frame.
type Frame struct {
	Data   []byte
	Width  int
	Height int
}

// FrameSource streams raw video frames from a running emulator.
type FrameSource interface {
	// Frames starts streaming and returns a channel of frames, closed when
	// ctx is cancelled or the underlying stream ends/errors. The channel is
	// "latest wins": a slow consumer never blocks the receive loop, it just
	// misses intermediate frames — matching the pacer's own latest-frame
	// semantics (see webrtc/pacer.go), not a queue.
	Frames(ctx context.Context) (<-chan Frame, error)
}

// GRPCFrameSource is the real FrameSource, dialing the emulator's gRPC
// server (the emulator is already launched with -grpc <port> — see
// runtime/androidstudio/launch.go) and calling streamScreenshot.
//
// Addr is "host:port", e.g. "127.0.0.1:8554". Width/Height request a
// downscaled capture (0, 0 requests the device's native resolution,
// matching ImageFormat's own "omit for no scaling" convention).
type GRPCFrameSource struct {
	Addr          string
	Width, Height int

	// dialOpts overrides the default dial options — unexported, set only by
	// tests (same package) to inject a bufconn dialer instead of a real
	// network connection. nil means "use the real insecure-loopback dial".
	dialOpts []grpc.DialOption
}

func (s *GRPCFrameSource) Frames(ctx context.Context) (<-chan Frame, error) {
	dialOpts := s.dialOpts
	if dialOpts == nil {
		dialOpts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(s.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial emulator grpc at %s: %w", s.Addr, err)
	}

	client := emupb.NewEmulatorControllerClient(conn)
	stream, err := client.StreamScreenshot(ctx, &emupb.ImageFormat{
		Format: emupb.ImageFormat_RGBA8888,
		Width:  uint32(s.Width),
		Height: uint32(s.Height),
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("start streamScreenshot at %s: %w", s.Addr, err)
	}

	frames := make(chan Frame, 1)
	go func() {
		defer close(frames)
		defer conn.Close()
		for {
			img, err := stream.Recv()
			if err != nil {
				if err != io.EOF && ctx.Err() == nil {
					log.Printf("emugrpc: streamScreenshot recv from %s failed: %v", s.Addr, err)
				}
				return
			}
			format := img.GetFormat()
			frame := Frame{
				Data:   img.GetImage(),
				Width:  int(format.GetWidth()),
				Height: int(format.GetHeight()),
			}
			// Latest-wins: drain a stale buffered frame before pushing, so
			// this send never blocks on a slow/absent consumer.
			select {
			case <-frames:
			default:
			}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	return frames, nil
}
