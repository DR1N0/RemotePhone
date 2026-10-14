package session

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestKeyedMutexSerializesSameKey(t *testing.T) {
	var m KeyedMutex
	var active int32
	var maxActive int32
	var wg sync.WaitGroup

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock := m.Lock("k")
			defer unlock()
			n := atomic.AddInt32(&active, 1)
			for {
				max := atomic.LoadInt32(&maxActive)
				if n <= max || atomic.CompareAndSwapInt32(&maxActive, max, n) {
					break
				}
			}
			time.Sleep(time.Millisecond)
			atomic.AddInt32(&active, -1)
		}()
	}
	wg.Wait()

	if maxActive != 1 {
		t.Errorf("max concurrent holders of the same key = %d, want 1", maxActive)
	}
}

func TestKeyedMutexAllowsDifferentKeysConcurrently(t *testing.T) {
	var m KeyedMutex
	var wg sync.WaitGroup
	start := make(chan struct{})
	results := make(chan bool, 2)

	for _, key := range []string{"a", "b"} {
		key := key
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			unlock := m.Lock(key)
			defer unlock()
			time.Sleep(20 * time.Millisecond)
			results <- true
		}()
	}
	close(start)
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("different keys appear to be serialized against each other")
	}
}

func TestKeyedMutexCleansUpEntries(t *testing.T) {
	var m KeyedMutex
	unlock := m.Lock("k")
	unlock()

	m.mu.Lock()
	n := len(m.entries)
	m.mu.Unlock()
	if n != 0 {
		t.Errorf("entries map has %d entries after unlock, want 0 (leaked)", n)
	}
}
