package webrtc

import (
	"encoding/binary"
	"testing"
)

// writeStereoPCM builds frames stereo S16LE samples at the given rate.
func writeStereoPCM(samples int, sampleRate int, left, right int16) []byte {
	out := make([]byte, samples*4)
	for i := 0; i < samples; i++ {
		binary.LittleEndian.PutUint16(out[i*4:], uint16(left))
		binary.LittleEndian.PutUint16(out[i*4+2:], uint16(right))
	}
	return out
}

func TestPCMUEncoderProduces20msFrames(t *testing.T) {
	// 20 ms at 48 kHz stereo is 960 input frames → exactly one 160-byte
	// 8 kHz mono μ-law packet.
	encoder := newPCMUEncoder()
	frames := encoder.Write(writeStereoPCM(960, 48000, 10000, 10000), 48000, 2)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if len(frames[0]) != pcmuFrameSamples {
		t.Fatalf("frame length = %d, want %d", len(frames[0]), pcmuFrameSamples)
	}
	for i, b := range frames[0] {
		if b == 0xFF {
			t.Fatalf("sample %d encoded as digital silence despite non-zero input", i)
		}
	}
}

func TestPCMUEncoderResamples48kTo8k(t *testing.T) {
	encoder := newPCMUEncoder()
	// One second of constant non-zero mono audio: 8000 bytes = 50 frames.
	var frames int
	for i := 0; i < 50; i++ {
		chunk := writeStereoPCM(960, 48000, 8000, 8000)
		frames += len(encoder.Write(chunk, 48000, 2))
	}
	if frames != 50 {
		t.Errorf("encoded %d frames for 1s at 48k, want 50", frames)
	}
}

func TestPCMUEncoderDownmixesStereo(t *testing.T) {
	encoder := newPCMUEncoder()
	// Left +max, right -max averages to silence-ish (0), which should encode
	// as the μ-law zero code 0xFF.
	frames := encoder.Write(writeStereoPCM(960, 48000, 32767, -32767), 48000, 2)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	if frames[0][0] != 0xFF {
		t.Errorf("opposite-phase stereo first byte = %#x, want 0xFF (μ-law zero)", frames[0][0])
	}
}

func TestPCMUEncoderHandlesPacketSizesThatOvershootTheResampler(t *testing.T) {
	// Real emulator packets are ~1088 audio frames, which is not a multiple
	// of 48k/8k=6. The resampler position can end up a few samples past the
	// buffered input; the drop path must clamp instead of panicking.
	encoder := newPCMUEncoder()
	var frames int
	for i := 0; i < 40; i++ {
		frames += len(encoder.Write(writeStereoPCM(1088, 48000, 4000, 4000), 48000, 2))
	}
	if frames == 0 {
		t.Fatal("no frames encoded from 40 emulator-sized packets")
	}
}

func TestPCMUEncoderResetsOnFormatChange(t *testing.T) {
	encoder := newPCMUEncoder()
	_ = encoder.Write(writeStereoPCM(100, 48000, 1000, 1000), 48000, 2)
	// A different rate must not reuse fractional resampler state or complete
	// a partial frame across formats: 400 stereo frames at 16 kHz is 25 ms,
	// i.e. exactly one 20 ms output frame plus a 5 ms remainder.
	frames := encoder.Write(writeStereoPCM(400, 16000, 1000, 1000), 16000, 2)
	if len(frames) != 1 {
		t.Errorf("got %d frames after format change, want 1", len(frames))
	}
}

func TestEncodeMuLawKnownValues(t *testing.T) {
	if got := encodeMuLaw(0); got != 0xFF {
		t.Errorf("encodeMuLaw(0) = %#x, want 0xFF", got)
	}
	if got := encodeMuLaw(1); got != 0x80 {
		t.Errorf("encodeMuLaw(1) = %#x, want 0x80", got)
	}
	if got := encodeMuLaw(-1); got != 0x00 {
		t.Errorf("encodeMuLaw(-1) = %#x, want 0x00", got)
	}
}
