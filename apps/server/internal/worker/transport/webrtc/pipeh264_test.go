package webrtc

import (
	"bytes"
	"testing"

	"github.com/pion/webrtc/v3/pkg/media"
)

// fakeSampleWriter records every WriteSample call verbatim — lets tests
// assert exactly how pipeH264 grouped NALs into access units without a
// real PeerConnection/track.
type fakeSampleWriter struct {
	samples []media.Sample
}

func (f *fakeSampleWriter) WriteSample(sample media.Sample) error {
	f.samples = append(f.samples, sample)
	return nil
}

// nal builds one Annex-B NAL unit (4-byte start code + header byte +
// payload) — annexBStream concatenates several to build a fake encoder
// output stream.
func nal(nalType byte, payload ...byte) []byte {
	out := []byte{0x00, 0x00, 0x00, 0x01, nalType}
	return append(out, payload...)
}

func annexBStream(nals ...[]byte) []byte {
	var out []byte
	for _, n := range nals {
		out = append(out, n...)
	}
	return out
}

// stripStartCode mirrors what pipeH264's scanner hands to addNAL — the raw
// NAL (header + payload), without its leading start code — for building
// expected results to compare against.
func withStartCode(nalBytes []byte) []byte {
	return append([]byte{0x00, 0x00, 0x00, 0x01}, nalBytes...)
}

func TestPipeH264GroupsOneAccessUnitIntoOneWriteSampleCall(t *testing.T) {
	sps := nal(0x07, 0xAA)   // type 7 = SPS
	pps := nal(0x08, 0xBB)   // type 8 = PPS
	sei := nal(0x06, 0xCC)   // type 6 = SEI (videotoolbox emits this per keyframe)
	slice := nal(0x05, 0xDD) // type 5 = IDR slice — ends the access unit

	stream := annexBStream(sps, pps, sei, slice)
	writer := &fakeSampleWriter{}
	pipeH264(bytes.NewReader(stream), writer, "test")

	if len(writer.samples) != 1 {
		t.Fatalf("WriteSample called %d times, want exactly 1 (one access unit)", len(writer.samples))
	}
	want := bytes.Join([][]byte{
		withStartCode([]byte{0x07, 0xAA}),
		withStartCode([]byte{0x08, 0xBB}),
		withStartCode([]byte{0x06, 0xCC}),
		withStartCode([]byte{0x05, 0xDD}),
	}, nil)
	if !bytes.Equal(writer.samples[0].Data, want) {
		t.Errorf("sample data = %x, want all 4 NALs concatenated with start codes = %x", writer.samples[0].Data, want)
	}
}

func TestPipeH264EmitsOneWriteSampleCallPerAccessUnit(t *testing.T) {
	// Two access units: the first has SPS+PPS+IDR slice (a keyframe), the
	// second is just a single non-IDR slice (the common steady-state case).
	first := annexBStream(nal(0x07, 0x01), nal(0x08, 0x02), nal(0x05, 0x03))
	second := nal(0x01, 0x04)
	writer := &fakeSampleWriter{}
	pipeH264(bytes.NewReader(annexBStream(first, second)), writer, "test")

	if len(writer.samples) != 2 {
		t.Fatalf("WriteSample called %d times, want exactly 2 (two access units)", len(writer.samples))
	}
	if !bytes.Contains(writer.samples[0].Data, []byte{0x05, 0x03}) {
		t.Errorf("first sample missing its IDR slice: %x", writer.samples[0].Data)
	}
	wantSecond := withStartCode([]byte{0x01, 0x04})
	if !bytes.Equal(writer.samples[1].Data, wantSecond) {
		t.Errorf("second sample = %x, want the lone non-IDR slice %x", writer.samples[1].Data, wantSecond)
	}
}

func TestPipeH264GroupsMultiSliceFrameIntoOneSample(t *testing.T) {
	// libx264 -tune zerolatency splits one picture into one slice per CPU
	// core. Every slice of that picture must land in a single Sample, or
	// pion advances the RTP timestamp once per slice and the stream runs
	// many times faster than real time. Byte 0x80 encodes
	// first_mb_in_slice=0 (new picture); 0x40 encodes first_mb_in_slice=1
	// (continuation slice of the same picture).
	sps := nal(0x07, 0xAA)
	pps := nal(0x08, 0xBB)
	slice0 := nal(0x05, 0x80, 0x11)
	slice1 := nal(0x05, 0x40, 0x22)
	nextFrame := nal(0x01, 0x80, 0x33)

	writer := &fakeSampleWriter{}
	pipeH264(bytes.NewReader(annexBStream(sps, pps, slice0, slice1, nextFrame)), writer, "test")

	if len(writer.samples) != 2 {
		t.Fatalf("WriteSample called %d times, want 2 (one per picture, not one per slice)", len(writer.samples))
	}
	wantFirst := bytes.Join([][]byte{
		withStartCode([]byte{0x07, 0xAA}),
		withStartCode([]byte{0x08, 0xBB}),
		withStartCode([]byte{0x05, 0x80, 0x11}),
		withStartCode([]byte{0x05, 0x40, 0x22}),
	}, nil)
	if !bytes.Equal(writer.samples[0].Data, wantFirst) {
		t.Errorf("first sample = %x, want both slices of picture one = %x", writer.samples[0].Data, wantFirst)
	}
	wantSecond := withStartCode([]byte{0x01, 0x80, 0x33})
	if !bytes.Equal(writer.samples[1].Data, wantSecond) {
		t.Errorf("second sample = %x, want %x", writer.samples[1].Data, wantSecond)
	}
}

func TestPipeH264FlushesTrailingNALWithoutAFollowingSlice(t *testing.T) {
	// A parameter-set NAL that never gets a following slice before the
	// stream ends (e.g. the encoder was killed mid-GOP) must still be
	// flushed, not silently dropped.
	stream := nal(0x07, 0xEE)
	writer := &fakeSampleWriter{}
	pipeH264(bytes.NewReader(stream), writer, "test")

	if len(writer.samples) != 1 {
		t.Fatalf("WriteSample called %d times, want exactly 1 (flushed trailing NAL)", len(writer.samples))
	}
	want := withStartCode([]byte{0x07, 0xEE})
	if !bytes.Equal(writer.samples[0].Data, want) {
		t.Errorf("flushed sample = %x, want %x", writer.samples[0].Data, want)
	}
}

func TestPipeH264EmitsNothingForEmptyStream(t *testing.T) {
	writer := &fakeSampleWriter{}
	pipeH264(bytes.NewReader(nil), writer, "test")
	if len(writer.samples) != 0 {
		t.Errorf("WriteSample called %d times for an empty stream, want 0", len(writer.samples))
	}
}
