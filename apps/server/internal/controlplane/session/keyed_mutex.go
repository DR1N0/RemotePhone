package session

import "sync"

// KeyedMutex serializes operations that share a key, without blocking
// operations on different keys. This replaces the TS
// attachment-transport-coordinator.ts's `run`/`runMany` for request-time
// use (session/transport methods that touch both the DB and a worker HTTP
// call for the same attachment, called synchronously from HTTP handlers) —
// NOT its background-sweep `runMany` usage in reconciler.ts/admin-service.ts,
// which the Go controller's single-item-at-a-time workqueue dispatch
// already replaces. See docs/plans/session-transport-redesign.md.
//
// SQLite's single-writer connection pool (see auth.NewService's pool-size-1
// comment) already serializes the *database* side of concurrent requests;
// KeyedMutex exists for the *worker HTTP calls* in between transactions,
// which that pool does not cover — two concurrent requests for the same
// attachment (e.g. a disconnect racing a promote) could otherwise reach the
// worker out of order.
//
// Per-key entries are reference-counted and removed once unused, so a
// long-running process doesn't accumulate one permanent mutex per
// attachment ID that ever existed.
type KeyedMutex struct {
	mu      sync.Mutex
	entries map[string]*keyedMutexEntry
}

type keyedMutexEntry struct {
	mu       sync.Mutex
	refCount int
}

// Lock blocks until key is available, then returns an unlock function.
// Typical use: `defer m.Lock(key)()`.
func (m *KeyedMutex) Lock(key string) func() {
	m.mu.Lock()
	if m.entries == nil {
		m.entries = make(map[string]*keyedMutexEntry)
	}
	entry, ok := m.entries[key]
	if !ok {
		entry = &keyedMutexEntry{}
		m.entries[key] = entry
	}
	entry.refCount++
	m.mu.Unlock()

	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		m.mu.Lock()
		entry.refCount--
		if entry.refCount == 0 {
			delete(m.entries, key)
		}
		m.mu.Unlock()
	}
}

// AttachmentKey is the standard key shape for serializing per-attachment
// operations: "<sessionID>:<attachmentID>", matching the TS coordinator's
// keyFor.
func AttachmentKey(sessionID, attachmentID string) string {
	return sessionID + ":" + attachmentID
}
