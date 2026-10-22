package emugrpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/types/known/emptypb"

	emupb "mako/internal/worker/transport/emugrpc/proto"
)

// fakeEmulatorServer streams a fixed sequence of images, then blocks until
// the context is cancelled — close enough to the real emulator's "keep
// streaming for the connection's lifetime" behavior for this test.
type fakeEmulatorServer struct {
	emupb.UnimplementedEmulatorControllerServer
	images []*emupb.Image
	audio  []*emupb.AudioPacket

	// touches records every SendTouch call for input tests.
	touches []*emupb.TouchEvent
}

func (s *fakeEmulatorServer) StreamScreenshot(_ *emupb.ImageFormat, stream grpc.ServerStreamingServer[emupb.Image]) error {
	for _, img := range s.images {
		if err := stream.Send(img); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return nil
}

func (s *fakeEmulatorServer) StreamAudio(_ *emupb.AudioFormat, stream grpc.ServerStreamingServer[emupb.AudioPacket]) error {
	for _, packet := range s.audio {
		if err := stream.Send(packet); err != nil {
			return err
		}
	}
	<-stream.Context().Done()
	return nil
}

func (s *fakeEmulatorServer) SendTouch(_ context.Context, event *emupb.TouchEvent) (*emptypb.Empty, error) {
	s.touches = append(s.touches, event)
	return &emptypb.Empty{}, nil
}

// startFakeServer starts fakeEmulatorServer on an in-memory bufconn listener
// and returns dial options that reach it — no real network port, no real
// emulator required. Shared by the screenshot, audio, and touch tests.
func startFakeServer(t *testing.T, srv *fakeEmulatorServer) []grpc.DialOption {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { lis.Close() })

	gs := grpc.NewServer()
	emupb.RegisterEmulatorControllerServer(gs, srv)
	go gs.Serve(lis)
	t.Cleanup(gs.Stop)

	// grpc.NewClient can't target a bufconn listener directly; a context
	// dialer override with a passthrough target is the standard workaround.
	return []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
}

func dialFakeServer(t *testing.T, srv *fakeEmulatorServer) *GRPCFrameSource {
	t.Helper()
	return &GRPCFrameSource{Addr: "passthrough:///bufnet", dialOpts: startFakeServer(t, srv)}
}

func TestGRPCFrameSourceStreamsFrames(t *testing.T) {
	srv := &fakeEmulatorServer{images: []*emupb.Image{
		{Image: []byte{1, 2, 3, 4}, Format: &emupb.ImageFormat{Width: 2, Height: 1}},
		{Image: []byte{5, 6, 7, 8}, Format: &emupb.ImageFormat{Width: 2, Height: 1}},
	}}
	source := dialFakeServer(t, srv)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	frames, err := source.Frames(ctx)
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}

	select {
	case frame := <-frames:
		if frame.Width != 2 || frame.Height != 1 || len(frame.Data) != 4 {
			t.Errorf("unexpected frame: %+v", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a frame")
	}
}

func TestGRPCFrameSourceClosesChannelOnContextCancel(t *testing.T) {
	srv := &fakeEmulatorServer{images: []*emupb.Image{
		{Image: []byte{1}, Format: &emupb.ImageFormat{Width: 1, Height: 1}},
	}}
	source := dialFakeServer(t, srv)

	ctx, cancel := context.WithCancel(context.Background())
	frames, err := source.Frames(ctx)
	if err != nil {
		t.Fatalf("Frames: %v", err)
	}

	<-frames // drain the one frame the fake server sends
	cancel()

	select {
	case _, ok := <-frames:
		if ok {
			t.Error("expected channel to close after context cancellation")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for channel to close")
	}
}
