package runtime

import "sync"

type HandleStore interface {
	Put(handle InstanceHandle)
	Get(instanceID string) (InstanceHandle, bool)
	Delete(instanceID string)
	List() []InstanceHandle
}

type MemoryHandleStore struct {
	mu      sync.RWMutex
	handles map[string]InstanceHandle
}

func NewMemoryHandleStore() *MemoryHandleStore {
	return &MemoryHandleStore{handles: make(map[string]InstanceHandle)}
}

func (s *MemoryHandleStore) Put(handle InstanceHandle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handles[handle.InstanceID] = handle
}

func (s *MemoryHandleStore) Get(instanceID string) (InstanceHandle, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	handle, ok := s.handles[instanceID]
	return handle, ok
}

func (s *MemoryHandleStore) Delete(instanceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.handles, instanceID)
}

func (s *MemoryHandleStore) List() []InstanceHandle {
	s.mu.RLock()
	defer s.mu.RUnlock()
	items := make([]InstanceHandle, 0, len(s.handles))
	for _, handle := range s.handles {
		items = append(items, handle)
	}
	return items
}
