package transport

import "sync"

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
