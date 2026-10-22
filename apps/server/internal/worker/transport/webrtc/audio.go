package webrtc

import (
	"encoding/binary"
	"math"
)

// PCMU is WebRTC's G.711 μ-law codec: 8000 Hz, mono, one byte per sample,
// 20 ms per RTP packet = 160 bytes. It is deliberately the first audio
// implementation here because it needs no cgo/Opus encoder, works with every
// WebRTC stack, and is good enough to prove the emulator-audio → RTP path
// end to end. Opus can replace it later without changing the capture layer.
const (
	pcmuSampleRate   = 8000
	pcmuFrameSamples = 160 // 20 ms at 8 kHz
)

// pcmuEncoder converts interleaved S16LE PCM at an arbitrary rate/channel
// count into 20 ms μ-law frames. It downmixes to mono and linearly resamples
// to 8 kHz. Linear interpolation is not a studio-grade anti-aliasing filter,
// but it is well above the quality the 8 kHz μ-law codec itself can carry.
type pcmuEncoder struct {
	inputRate int
	channels  int

	samples []float64 // mono input samples not yet consumed by the resampler
	pos     float64   // fractional read position within samples
	pending []byte    // encoded bytes not yet emitted as a full frame
}

func newPCMUEncoder() *pcmuEncoder { return &pcmuEncoder{} }

// Write consumes one PCM chunk and returns zero or more complete 160-byte
// μ-law frames. A chunk that doesn't fill a frame leaves the remainder
// buffered for the next call.
func (e *pcmuEncoder) Write(data []byte, sampleRate, channels int) [][]byte {
	if sampleRate <= 0 || channels <= 0 || len(data) < 2 {
		return nil
	}
	if sampleRate != e.inputRate || channels != e.channels {
		// Format changed (or first packet): resampler state from the old
		// rate is meaningless, and a partial frame from the old format must
		// not be completed with samples from the new one.
		e.inputRate, e.channels = sampleRate, channels
		e.samples = e.samples[:0]
		e.pos = 0
		e.pending = e.pending[:0]
	}

	frameCount := len(data) / (2 * channels)
	for i := 0; i < frameCount; i++ {
		var sum int32
		for c := 0; c < channels; c++ {
			offset := (i*channels + c) * 2
			sum += int32(int16(binary.LittleEndian.Uint16(data[offset:])))
		}
		e.samples = append(e.samples, float64(sum)/float64(channels)/32768.0)
	}

	step := float64(e.inputRate) / float64(pcmuSampleRate)
	for e.pos+1 < float64(len(e.samples)) {
		idx := int(e.pos)
		frac := e.pos - float64(idx)
		value := e.samples[idx]*(1-frac) + e.samples[idx+1]*frac
		e.pending = append(e.pending, encodeMuLaw(value))
		e.pos += step
	}

	// Drop consumed samples so a long-lived stream doesn't grow unbounded.
	// The loop above can leave pos slightly past len(samples) (up to one
	// resample step), so clamp the drop: an emulator packet size that isn't
	// an exact multiple of the resample ratio otherwise panics here.
	drop := int(e.pos)
	if drop > len(e.samples) {
		drop = len(e.samples)
	}
	if drop > 4096 {
		e.samples = append(e.samples[:0], e.samples[drop:]...)
		e.pos -= float64(drop)
	}

	var frames [][]byte
	for len(e.pending) >= pcmuFrameSamples {
		frame := make([]byte, pcmuFrameSamples)
		copy(frame, e.pending[:pcmuFrameSamples])
		e.pending = e.pending[pcmuFrameSamples:]
		frames = append(frames, frame)
	}
	return frames
}

// encodeMuLaw implements the standard G.711 μ-law companding curve
// (ITU-T G.711, 8-bit, bias 0x84, clip 32635).
func encodeMuLaw(sample float64) byte {
	if sample > 1 {
		sample = 1
	} else if sample < -1 {
		sample = -1
	}
	value := int(math.Round(sample * 32767))
	sign := 0
	if value < 0 {
		value = -value
		sign = 0x80
	}
	if value > 32635 {
		value = 32635
	}
	value += 0x84

	exponent := 7
	for mask := 0x4000; exponent > 0 && value&mask == 0; mask >>= 1 {
		exponent--
	}
	mantissa := (value >> (exponent + 3)) & 0x0F
	return ^byte(sign | exponent<<4 | mantissa)
}
