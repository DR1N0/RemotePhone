package emugrpc

import (
	"context"
	"fmt"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	emupb "remotephone/internal/worker/transport/emugrpc/proto"
)

// TouchPoint is one contact in a TouchEvent, in physical display pixels.
// Pressure must be non-zero while the finger is down and zero on the final
// lift event; the emulator uses pressure==0 to unregister the identifier.
type TouchPoint struct {
	ID         int32
	X          int32
	Y          int32
	Pressure   int32
	TouchMajor int32
	TouchMinor int32
}

// TouchSender injects touch events into a running emulator. The real
// implementation uses the emulator's gRPC sendTouch RPC, which is
// dramatically lower latency than spawning `adb shell input` per event and
// supports multiple simultaneous pointers.
type TouchSender interface {
	SendTouch(ctx context.Context, points []TouchPoint) error
	Close() error
}

// GRPCTouchSender is the real TouchSender. It keeps one lazily-dialed gRPC
// connection for the adapter's lifetime.
type GRPCTouchSender struct {
	Addr     string
	dialOpts []grpc.DialOption

	mu     sync.Mutex
	conn   *grpc.ClientConn
	client emupb.EmulatorControllerClient
}

func NewGRPCTouchSender(addr string) *GRPCTouchSender {
	return &GRPCTouchSender{Addr: addr}
}

func (s *GRPCTouchSender) getClient() (emupb.EmulatorControllerClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	dialOpts := s.dialOpts
	if dialOpts == nil {
		dialOpts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(s.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial emulator grpc at %s: %w", s.Addr, err)
	}
	s.conn = conn
	s.client = emupb.NewEmulatorControllerClient(conn)
	return s.client, nil
}

func (s *GRPCTouchSender) SendTouch(ctx context.Context, points []TouchPoint) error {
	client, err := s.getClient()
	if err != nil {
		return err
	}
	touches := make([]*emupb.Touch, 0, len(points))
	for _, p := range points {
		touches = append(touches, &emupb.Touch{
			X:          p.X,
			Y:          p.Y,
			Identifier: p.ID,
			Pressure:   p.Pressure,
			TouchMajor: p.TouchMajor,
			TouchMinor: p.TouchMinor,
		})
	}
	if _, err := client.SendTouch(ctx, &emupb.TouchEvent{Touches: touches}); err != nil {
		return fmt.Errorf("sendTouch: %w", err)
	}
	return nil
}

func (s *GRPCTouchSender) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		err := s.conn.Close()
		s.conn = nil
		s.client = nil
		return err
	}
	return nil
}
