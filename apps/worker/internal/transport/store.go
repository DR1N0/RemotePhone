package transport

import (
	"strings"
	"sync"
)

// Store holds active TransportAdapter instances keyed by "instanceID:attachmentID".
type Store struct {
	mu       sync.RWMutex
	adapters map[string]Adapter
}

func NewStore() *Store {
	return &Store{adapters: make(map[string]Adapter)}
}

func (s *Store) Put(key string, a Adapter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters[key] = a
}

func (s *Store) Get(key string) (Adapter, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	a, ok := s.adapters[key]
	return a, ok
}

func (s *Store) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.adapters, key)
}

// StopAndDelete stops and removes the adapter for key.
func (s *Store) StopAndDelete(key string) {
	s.mu.Lock()
	adapter, ok := s.adapters[key]
	delete(s.adapters, key)
	s.mu.Unlock()
	if ok {
		_ = adapter.Stop()
	}
}

// StopAndDeleteByPrefix stops and removes all adapters whose key has the given prefix.
func (s *Store) StopAndDeleteByPrefix(prefix string) {
	s.mu.Lock()
	var toStop []Adapter
	var toDelete []string
	for k, a := range s.adapters {
		if strings.HasPrefix(k, prefix) {
			toStop = append(toStop, a)
			toDelete = append(toDelete, k)
		}
	}
	for _, k := range toDelete {
		delete(s.adapters, k)
	}
	s.mu.Unlock()
	// Stop adapters outside the lock to avoid holding it during Stop() calls.
	for _, a := range toStop {
		_ = a.Stop()
	}
}
