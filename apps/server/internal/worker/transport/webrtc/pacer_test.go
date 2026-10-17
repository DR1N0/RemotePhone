package webrtc

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeFrameWriter records every frame handed to it, optionally blocking or
// erroring on demand — lets tests exercise the pacer without a real ffmpeg
// subprocess.
type fakeFrameWriter struct {
	mu      sync.Mutex
	writes  [][]byte
	block   chan struct{} // if non-nil, Write blocks until this is closed
	failErr error
}

func (f *fakeFrameWriter) Write(frame []byte) (int, error) {
	if f.block != nil {
		<-f.block
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return 0, f.failErr
	}
	cp := append([]byte(nil), frame...)
	f.writes = append(f.writes, cp)
	return len(frame), nil
}

func (f *fakeFrameWriter) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.writes)
}

func (f *fakeFrameWriter) lastWrite() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.writes) == 0 {
		return nil
	}
	return f.writes[len(f.writes)-1]
}

func TestPacerWritesLatestFrameOnEveryTick(t *testing.T) {
	writer := &fakeFrameWriter{}
	pacer := NewPacer(writer, 100, nil) // 10ms ticks, fast for the test
	defer pacer.Stop()

	pacer.Update([]byte{1, 2, 3})
	time.Sleep(50 * time.Millisecond)

	if writer.writeCount() < 2 {
		t.Errorf("expected multiple ticks to have written the frame, got %d writes", writer.writeCount())
	}
	if got := writer.lastWrite(); string(got) != string([]byte{1, 2, 3}) {
		t.Errorf("last write = %v, want [1 2 3]", got)
	}
}

func TestPacerDuplicatesLastFrameWhenNothingNew(t *testing.T) {
	writer := &fakeFrameWriter{}
	pacer := NewPacer(writer, 100, nil)
	defer pacer.Stop()

	pacer.Update([]byte{9})
	time.Sleep(60 * time.Millisecond)

	count := writer.writeCount()
	if count < 3 {
		t.Fatalf("expected several duplicate writes of the one frame, got %d", count)
	}
	for i := 0; i < count; i++ {
		writer.mu.Lock()
		got := writer.writes[i]
		writer.mu.Unlock()
		if len(got) != 1 || got[0] != 9 {
			t.Errorf("write[%d] = %v, want [9] (duplicated unchanged)", i, got)
		}
	}
}

func TestPacerWritesNothingBeforeFirstUpdate(t *testing.T) {
	writer := &fakeFrameWriter{}
	pacer := NewPacer(writer, 100, nil)
	time.Sleep(30 * time.Millisecond)
	pacer.Stop()

	if writer.writeCount() != 0 {
		t.Errorf("expected no writes before Update is ever called, got %d", writer.writeCount())
	}
}

func TestPacerReportsStallOnWriteError(t *testing.T) {
	writer := &fakeFrameWriter{failErr: errors.New("broken pipe")}
	stalled := make(chan error, 1)
	pacer := NewPacer(writer, 100, func(err error) { stalled <- err })
	defer pacer.Stop()

	pacer.Update([]byte{1})

	select {
	case err := <-stalled:
		if err == nil {
			t.Error("expected a non-nil error on the stall callback")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for onStall to fire after a write error")
	}
}

func TestPacerReportsStallOnBlockedWrite(t *testing.T) {
	block := make(chan struct{}) // never closed — write blocks forever
	writer := &fakeFrameWriter{block: block}
	stalled := make(chan error, 1)
	pacer := NewPacer(writer, 1000, func(err error) { stalled <- err }) // fast ticks; the blocked write stalls regardless of tick rate
	pacer.Update([]byte{1})
	defer close(block) // unblock the leaked write goroutine so the test process can exit cleanly

	select {
	case err := <-stalled:
		if err != errStalledWrite {
			t.Errorf("err = %v, want errStalledWrite", err)
		}
	case <-time.After(pacerStallTimeout + 2*time.Second):
		t.Fatal("timed out waiting for onStall to fire after a blocked write")
	}
}
