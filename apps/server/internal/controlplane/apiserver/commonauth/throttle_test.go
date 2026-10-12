package commonauth

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginThrottleAllowsUpToLimit(t *testing.T) {
	th := newLoginThrottle()
	now := time.Now()
	for i := 0; i < 4; i++ {
		if th.tooManyFailures("user:x", now) {
			t.Fatalf("throttled after only %d failures, limit is 5", i)
		}
		th.recordFailure("user:x", now)
	}
	if th.tooManyFailures("user:x", now) {
		t.Error("throttled at exactly 4 recorded failures, want not yet throttled")
	}
	th.recordFailure("user:x", now)
	if !th.tooManyFailures("user:x", now) {
		t.Error("not throttled after 5 recorded failures, want throttled")
	}
}

func TestLoginThrottleWindowSlides(t *testing.T) {
	th := newLoginThrottle()
	base := time.Now()
	for i := 0; i < 5; i++ {
		th.recordFailure("user:x", base)
	}
	if !th.tooManyFailures("user:x", base) {
		t.Fatal("not throttled immediately after 5 failures")
	}
	later := base.Add(11 * time.Minute)
	if th.tooManyFailures("user:x", later) {
		t.Error("still throttled 11 minutes later, want the window to have expired")
	}
}

func TestLoginThrottleKeysAreIndependent(t *testing.T) {
	th := newLoginThrottle()
	now := time.Now()
	for i := 0; i < 5; i++ {
		th.recordFailure("user:x", now)
	}
	if th.tooManyFailures("user:y", now) {
		t.Error("a different key was throttled by another key's failures")
	}
	if th.tooManyFailures("addr:<IP_ADDRESS>", now) {
		t.Error("an address key was throttled by a username key's failures")
	}
}

func TestLoginThrottleEvictsOldestKeyWhenCapExceeded(t *testing.T) {
	th := newLoginThrottle()
	now := time.Now()
	for i := 0; i < maxTrackedLoginKeys; i++ {
		th.recordFailure(keyForIndex(i), now)
	}
	// The very first key recorded should have been evicted to make room for
	// the (maxTrackedLoginKeys)'th new key below.
	th.recordFailure("one-too-many", now)

	th.mu.Lock()
	_, stillTracked := th.failures[keyForIndex(0)]
	trackedCount := len(th.failures)
	th.mu.Unlock()

	if stillTracked {
		t.Error("oldest-inserted key was not evicted when the tracked-key cap was exceeded")
	}
	if trackedCount > maxTrackedLoginKeys {
		t.Errorf("tracked key count = %d, want capped at %d", trackedCount, maxTrackedLoginKeys)
	}
}

func keyForIndex(i int) string {
	return fmt.Sprintf("user:%d", i)
}
