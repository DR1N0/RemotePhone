package emugrpc

import (
	"context"
	"fmt"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	emupb "mako/internal/worker/transport/emugrpc/proto"
)

// AudioFrame is one chunk of raw PCM audio from the emulator. Data is
// little-endian signed 16-bit (S16LE), interleaved by channel.
type AudioFrame struct {
	Data       []byte
	SampleRate int
	Channels   int
}

// AudioSource streams raw PCM audio from a running emulator.
type AudioSource interface {
	// Packets starts streaming and returns a channel closed when ctx is
	// cancelled or the underlying stream ends. Unlike video frames, audio is
	// never dropped deliberately: a slow consumer applies backpressure to the
	// gRPC stream, because dropping samples is an audible glitch.
	Packets(ctx context.Context) (<-chan AudioFrame, error)
}

// GRPCAudioSource is the real AudioSource, using the emulator's streamAudio
// RPC. The requested format is S16LE at SampleRate/Channels; the emulator's
// per-packet format (if present) is reported on each AudioFrame so the
// converter can adapt when it differs.
type GRPCAudioSource struct {
	Addr       string
	SampleRate int // 0 defaults to 48000
	Channels   int // 1 mono, 2 stereo; 0 defaults to 2

	// dialOpts overrides the default dial options — unexported, set only by
	// tests (same package) to inject a bufconn dialer.
	dialOpts []grpc.DialOption
}

func (s *GRPCAudioSource) Packets(ctx context.Context) (<-chan AudioFrame, error) {
	dialOpts := s.dialOpts
	if dialOpts == nil {
		dialOpts = []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	}
	conn, err := grpc.NewClient(s.Addr, dialOpts...)
	if err != nil {
		return nil, fmt.Errorf("dial emulator grpc at %s: %w", s.Addr, err)
	}

	requestedRate := s.SampleRate
	if requestedRate <= 0 {
		requestedRate = 48000
	}
	requestedChannels := s.Channels
	if requestedChannels <= 0 {
		requestedChannels = 2
	}

	channels := emupb.AudioFormat_Mono
	if requestedChannels == 2 {
		channels = emupb.AudioFormat_Stereo
	}
	client := emupb.NewEmulatorControllerClient(conn)
	stream, err := client.StreamAudio(ctx, &emupb.AudioFormat{
		SamplingRate: uint64(requestedRate),
		Channels:     channels,
		Format:       emupb.AudioFormat_AUD_FMT_S16,
		// Leave Mode at MODE_UNSPECIFIED. MODE_REAL_TIME is documented as
		// experimental for audio injection; no evidence showed it helps
		// streamAudio, so don't opt into an experimental delivery mode.
	})
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("start streamAudio at %s: %w", s.Addr, err)
	}

	frames := make(chan AudioFrame, 32)
	go func() {
		defer close(frames)
		defer conn.Close()
		for {
			packet, err := stream.Recv()
			if err != nil {
				if ctx.Err() == nil {
					log.Printf("emugrpc: streamAudio recv from %s failed: %v", s.Addr, err)
				}
				return
			}
			if len(packet.GetAudio()) == 0 {
				continue
			}
			rate := int(packet.GetFormat().GetSamplingRate())
			if rate <= 0 {
				rate = requestedRate
			}
			ch := requestedChannels
			if format := packet.GetFormat(); format != nil {
				if format.GetChannels() == emupb.AudioFormat_Stereo {
					ch = 2
				} else {
					ch = 1
				}
			}
			frame := AudioFrame{Data: packet.GetAudio(), SampleRate: rate, Channels: ch}
			select {
			case frames <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	return frames, nil
}
