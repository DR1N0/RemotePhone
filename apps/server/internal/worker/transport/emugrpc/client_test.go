package emugrpc

import (
	"context"
	"net"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	emupb "mako/internal/worker/transport/emugrpc/proto"
)

// fakeEmulatorServer streams a fixed sequence of images, then blocks until
// the context is cancelled — close enough to the real emulator's "keep
// streaming for the connection's lifetime" behavior for this test.
type fakeEmulatorServer struct {
	emupb.UnimplementedEmulatorControllerServer
	images []*emupb.Image
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

// dialFakeServer starts fakeEmulatorServer on an in-memory bufconn listener
// and returns a GRPCFrameSource dialed against it — no real network port,
// no real emulator required.
func dialFakeServer(t *testing.T, srv *fakeEmulatorServer) *GRPCFrameSource {
	t.Helper()
	lis := bufconn.Listen(1024 * 1024)
	t.Cleanup(func() { lis.Close() })

	gs := grpc.NewServer()
	emupb.RegisterEmulatorControllerServer(gs, srv)
	go gs.Serve(lis)
	t.Cleanup(gs.Stop)

	// GRPCFrameSource.Frames dials by address string via grpc.NewClient,
	// which can't target a bufconn listener directly — so this test talks
	// to the fake server through a real client built the same way
	// GRPCFrameSource does, just with a bufconn dialer, and exercises the
	// receive-loop logic by constructing the FrameSource around that
	// pre-dialed connection's target. grpc.NewClient resolves lazily, so
	// passing "passthrough:///bufnet" with a custom resolver isn't needed:
	// we instead dial with a context dialer override via grpc.WithContextDialer.
	return &GRPCFrameSource{Addr: "passthrough:///bufnet", dialOpts: []grpc.DialOption{
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}}
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
