package emugrpc

import (
	"context"
	"testing"
	"time"

	emupb "mako/internal/worker/transport/emugrpc/proto"
)

func TestGRPCAudioSourceStreamsPCM(t *testing.T) {
	srv := &fakeEmulatorServer{audio: []*emupb.AudioPacket{
		{
			Audio:  []byte{1, 2, 3, 4},
			Format: &emupb.AudioFormat{SamplingRate: 48000, Channels: emupb.AudioFormat_Stereo},
		},
		{
			Audio:  []byte{5, 6},
			Format: &emupb.AudioFormat{SamplingRate: 48000, Channels: emupb.AudioFormat_Mono},
		},
	}}
	source := &GRPCAudioSource{Addr: "passthrough:///bufnet", dialOpts: startFakeServer(t, srv)}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	packets, err := source.Packets(ctx)
	if err != nil {
		t.Fatalf("Packets: %v", err)
	}

	first := <-packets
	if string(first.Data) != string([]byte{1, 2, 3, 4}) {
		t.Errorf("first packet data = %v", first.Data)
	}
	if first.SampleRate != 48000 || first.Channels != 2 {
		t.Errorf("first packet format = %dHz/%dch, want 48000/2", first.SampleRate, first.Channels)
	}

	second := <-packets
	if second.Channels != 1 {
		t.Errorf("second packet channels = %d, want 1", second.Channels)
	}
}

func TestGRPCTouchSenderSendsMultiTouch(t *testing.T) {
	srv := &fakeEmulatorServer{}
	sender := &GRPCTouchSender{Addr: "passthrough:///bufnet", dialOpts: startFakeServer(t, srv)}
	defer sender.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	points := []TouchPoint{
		{ID: 1, X: 100, Y: 200, Pressure: 1},
		{ID: 2, X: 300, Y: 400, Pressure: 0},
	}
	if err := sender.SendTouch(ctx, points); err != nil {
		t.Fatalf("SendTouch: %v", err)
	}

	if len(srv.touches) != 1 {
		t.Fatalf("server received %d touch events, want 1", len(srv.touches))
	}
	touches := srv.touches[0].GetTouches()
	if len(touches) != 2 {
		t.Fatalf("event carried %d touches, want 2", len(touches))
	}
	if touches[0].GetIdentifier() != 1 || touches[0].GetX() != 100 || touches[0].GetY() != 200 || touches[0].GetPressure() != 1 {
		t.Errorf("first touch = %+v", touches[0])
	}
	if touches[1].GetIdentifier() != 2 || touches[1].GetPressure() != 0 {
		t.Errorf("second touch = %+v", touches[1])
	}
}
