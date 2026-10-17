package webrtc

import (
	"log"
	"sync/atomic"
	"time"
)

// frameWriter is the write half of an Encoder — satisfied by *Encoder, and
// by a fake in tests so the pacer is testable without a real ffmpeg
// subprocess.
type frameWriter interface {
	Write(frame []byte) (int, error)
}

// pacerStallTimeout bounds how long a single stdin write may block before
// the pacer gives up on this encoder and asks to be restarted — an ffmpeg
// process wedged mid-encode must not be allowed to block the pacer (and
// therefore the frame source behind it) indefinitely.
const pacerStallTimeout = 2 * time.Second

// Pacer bridges emugrpc's event-driven frame delivery (a new frame only
// when the on-device screen actually changes) into the fixed-rate stream
// ffmpeg's rawvideo stdin demuxer requires. It keeps only the single latest
// frame — deliberately not a queue, which would reintroduce the unbounded,
// compounding latency this whole capture redesign exists to eliminate —
// and writes it to the encoder on every tick, duplicating verbatim when
// nothing new arrived since the last tick.
type Pacer struct {
	latest atomic.Pointer[[]byte]

	stop    chan struct{}
	done    chan struct{}
	onStall func(error) // called at most once, from the pacer goroutine, if a write stalls or errors
}

// NewPacer starts ticking immediately at the given fps, writing whatever's
// in Update's latest frame to w. onStall is invoked (once) if a write to w
// blocks past pacerStallTimeout or returns an error — the caller should
// treat this as "the encoder is dead," stop the pacer, and restart capture.
// onStall may be nil.
func NewPacer(w frameWriter, fps int, onStall func(error)) *Pacer {
	p := &Pacer{
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		onStall: onStall,
	}
	go p.run(w, fps)
	return p
}

// Update sets the latest frame to write on the next tick. Safe to call
// concurrently with the pacer's own tick loop — never blocks.
func (p *Pacer) Update(frame []byte) {
	p.latest.Store(&frame)
}

func (p *Pacer) run(w frameWriter, fps int) {
	defer close(p.done)
	ticker := time.NewTicker(time.Second / time.Duration(fps))
	defer ticker.Stop()

	for {
		select {
		case <-p.stop:
			return
		case <-ticker.C:
			framePtr := p.latest.Load()
			if framePtr == nil {
				continue // no frame received yet
			}
			if err := p.writeWithTimeout(w, *framePtr); err != nil {
				if p.onStall != nil {
					p.onStall(err)
				}
				return
			}
		}
	}
}

// writeWithTimeout runs w.Write on its own goroutine and waits up to
// pacerStallTimeout — plain os/exec stdin pipes don't support
// SetWriteDeadline, so a bounded wait on a background goroutine is the
// simplest way to detect a wedged encoder without blocking the tick loop
// forever. The goroutine is intentionally allowed to leak if the write
// never returns (orphaned after the Encoder is killed and its stdin pipe
// closes, which unblocks it) — a wedged os.Process is an exceptional case,
// not a steady-state cost.
func (p *Pacer) writeWithTimeout(w frameWriter, frame []byte) error {
	result := make(chan error, 1)
	go func() {
		_, err := w.Write(frame)
		result <- err
	}()
	select {
	case err := <-result:
		return err
	case <-time.After(pacerStallTimeout):
		log.Printf("pacer: write stalled past %v, treating encoder as dead", pacerStallTimeout)
		return errStalledWrite
	}
}

var errStalledWrite = stallError("pacer: encoder write stalled")

type stallError string

func (e stallError) Error() string { return string(e) }

// Stop halts the tick loop and waits for it to exit.
func (p *Pacer) Stop() {
	close(p.stop)
	<-p.done
}
