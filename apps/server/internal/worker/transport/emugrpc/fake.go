package emugrpc

import "context"

// FakeFrameSource is a test double for FrameSource: it hands back
// whatever's pushed to its Push method instead of dialing a real emulator,
// so tests can drive the pacer/encoder pipeline deterministically. Exported
// (not test-only) so other packages' tests can reuse it, same reasoning as
// workerproc.FakeExecer.
type FakeFrameSource struct {
	frames chan Frame
}

// NewFakeFrameSource returns a ready-to-use fake; call Push to deliver
// frames once a consumer has called Frames.
func NewFakeFrameSource() *FakeFrameSource {
	return &FakeFrameSource{frames: make(chan Frame, 1)}
}

func (f *FakeFrameSource) Frames(ctx context.Context) (<-chan Frame, error) {
	return f.frames, nil
}

// Push delivers one frame, dropping any unconsumed buffered frame first —
// mirrors GRPCFrameSource's own latest-wins, never-blocks send semantics.
func (f *FakeFrameSource) Push(frame Frame) {
	select {
	case <-f.frames:
	default:
	}
	f.frames <- frame
}

// Close signals end-of-stream to any consumer ranging over Frames' channel.
func (f *FakeFrameSource) Close() {
	close(f.frames)
}
