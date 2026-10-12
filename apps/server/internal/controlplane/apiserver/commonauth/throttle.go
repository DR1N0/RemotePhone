package commonauth

import (
	"sync"
	"time"
)

// loginFailureLimit, loginFailureWindow, and maxTrackedLoginKeys mirror the
// TS constants LOGIN_FAILURE_LIMIT / LOGIN_FAILURE_WINDOW_MS /
// MAX_TRACKED_LOGIN_KEYS exactly.
const (
	loginFailureLimit   = 5
	loginFailureWindow  = 10 * time.Minute
	maxTrackedLoginKeys = 10_000
)

// loginThrottle tracks recent login failures per key (e.g. "user:alice" or
// "addr:10.0.0.5") to rate-limit repeated bad attempts. It is a faithful
// port of the TS route module's private Map<string, number[]> design: a
// sliding window of failure timestamps per key, a full prune sweep on every
// write, and insertion-order (not LRU) eviction once the tracked-key cap is
// exceeded. All state is guarded by mu since it is shared, concurrently
// accessed across every request hitting either HTTP surface.
type loginThrottle struct {
	mu       sync.Mutex
	failures map[string][]time.Time
	// order records key insertion order so recordFailure can evict the
	// oldest-inserted key first when the cap is exceeded — matching the TS
	// Map's iteration-order guarantee, not a last-used/LRU policy.
	order []string
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{failures: make(map[string][]time.Time)}
}

// recentTimestamps returns the subset of timestamps strictly after cutoff,
// never mutating timestamps' backing array.
func recentTimestamps(timestamps []time.Time, cutoff time.Time) []time.Time {
	recent := make([]time.Time, 0, len(timestamps))
	for _, ts := range timestamps {
		if ts.After(cutoff) {
			recent = append(recent, ts)
		}
	}
	return recent
}

// deleteLocked removes key from both failures and order. Caller must hold mu.
func (t *loginThrottle) deleteLocked(key string) {
	delete(t.failures, key)
	for i, k := range t.order {
		if k == key {
			t.order = append(t.order[:i], t.order[i+1:]...)
			break
		}
	}
}

// pruneAllLocked drops every failure timestamp older than loginFailureWindow
// (relative to now) across every tracked key, deleting any key left with
// zero timestamps. This mirrors the TS source's pruneLoginFailures, which
// sweeps the whole map on every recorded failure — not just the key being
// written. Caller must hold mu.
func (t *loginThrottle) pruneAllLocked(now time.Time) {
	cutoff := now.Add(-loginFailureWindow)
	for key, timestamps := range t.failures {
		recent := recentTimestamps(timestamps, cutoff)
		if len(recent) == 0 {
			t.deleteLocked(key)
		} else {
			t.failures[key] = recent
		}
	}
}

// tooManyFailures reports whether key has at least loginFailureLimit
// recorded failures within the last loginFailureWindow, as of now. The
// window is genuinely sliding: stale timestamps for key are pruned as part
// of this check (and the key dropped entirely once empty), never relying on
// a fixed bucket boundary.
func (t *loginThrottle) tooManyFailures(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	timestamps, ok := t.failures[key]
	if !ok {
		return false
	}
	cutoff := now.Add(-loginFailureWindow)
	recent := recentTimestamps(timestamps, cutoff)
	if len(recent) == 0 {
		t.deleteLocked(key)
		return false
	}
	t.failures[key] = recent
	return len(recent) >= loginFailureLimit
}

// recordFailure records a failed login attempt against key at time now. It
// first prunes every tracked key's stale timestamps (matching the TS
// source's full-sweep-per-write behavior), then — only if key is not
// already tracked and tracking it would exceed maxTrackedLoginKeys — evicts
// the oldest-inserted key before adding the new one.
func (t *loginThrottle) recordFailure(key string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.pruneAllLocked(now)

	_, alreadyTracked := t.failures[key]
	if !alreadyTracked && len(t.failures) >= maxTrackedLoginKeys && len(t.order) > 0 {
		t.deleteLocked(t.order[0])
	}
	if !alreadyTracked {
		t.order = append(t.order, key)
	}
	t.failures[key] = append(t.failures[key], now)
}
