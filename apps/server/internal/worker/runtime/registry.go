package runtime

import (
	"errors"
	"fmt"
	"sort"
)

type AdapterInfo struct {
	RuntimeClass string `json:"runtimeClass"`
	DisplayName  string `json:"displayName"`
}

type Registry struct {
	adapters map[string]Adapter
	handles  HandleStore
}

func NewRegistry(adapters ...Adapter) *Registry {
	return NewRegistryWithHandles(nil, adapters...)
}

func NewRegistryWithHandles(handles HandleStore, adapters ...Adapter) *Registry {
	registry := &Registry{adapters: make(map[string]Adapter), handles: handles}
	for _, adapter := range adapters {
		_ = registry.Register(adapter)
	}
	return registry
}

func (r *Registry) Register(adapter Adapter) error {
	if adapter == nil {
		return errors.New("runtime adapter is nil")
	}

	runtimeClass := adapter.RuntimeClass()
	if runtimeClass == "" {
		return errors.New("runtime adapter class is empty")
	}
	if _, exists := r.adapters[runtimeClass]; exists {
		return fmt.Errorf("runtime adapter already registered: %s", runtimeClass)
	}

	r.adapters[runtimeClass] = adapter
	return nil
}

func (r *Registry) Get(runtimeClass string) (Adapter, bool) {
	adapter, ok := r.adapters[runtimeClass]
	return adapter, ok
}

func (r *Registry) ListAdapters() []AdapterInfo {
	keys := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]AdapterInfo, 0, len(keys))
	for _, key := range keys {
		adapter := r.adapters[key]
		items = append(items, AdapterInfo{
			RuntimeClass: adapter.RuntimeClass(),
			DisplayName:  adapter.DisplayName(),
		})
	}

	return items
}

func (r *Registry) DiscoverTemplates() ([]Template, error) {
	keys := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]Template, 0)
	for _, key := range keys {
		templates, err := r.adapters[key].DiscoverTemplates()
		if err != nil {
			return nil, err
		}
		items = append(items, templates...)
	}

	sort.Slice(items, func(i, j int) bool {
		return items[i].TemplateID < items[j].TemplateID
	})

	return items, nil
}

func (r *Registry) Start(req StartRequest) (InstanceHandle, error) {
	adapter, ok := r.Get(req.RuntimeClass)
	if !ok {
		return InstanceHandle{}, ErrAdapterNotFound
	}
	return adapter.Start(req)
}

func (r *Registry) Inspect(instanceID string) (InstanceStatus, error) {
	if r.handles != nil {
		handle, ok := r.handles.Get(instanceID)
		if !ok {
			return InstanceStatus{}, ErrInstanceNotFound
		}
		adapter, ok := r.Get(handle.RuntimeClass)
		if !ok {
			return InstanceStatus{}, ErrAdapterNotFound
		}
		return adapter.Inspect(instanceID)
	}

	keys := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		status, err := r.adapters[key].Inspect(instanceID)
		if err == nil {
			return status, nil
		}
		if errors.Is(err, ErrInstanceNotFound) {
			continue
		}
		return InstanceStatus{}, err
	}

	return InstanceStatus{}, ErrInstanceNotFound
}

func (r *Registry) Stop(instanceID string) (InstanceStatus, error) {
	if r.handles != nil {
		handle, ok := r.handles.Get(instanceID)
		if !ok {
			return InstanceStatus{}, ErrInstanceNotFound
		}
		adapter, ok := r.Get(handle.RuntimeClass)
		if !ok {
			return InstanceStatus{}, ErrAdapterNotFound
		}
		return adapter.Stop(instanceID)
	}

	keys := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		status, err := r.adapters[key].Stop(instanceID)
		if err == nil {
			return status, nil
		}
		if errors.Is(err, ErrInstanceNotFound) {
			continue
		}
		return InstanceStatus{}, err
	}

	return InstanceStatus{}, ErrInstanceNotFound
}

func (r *Registry) Reconcile() ([]InstanceStatus, error) {
	keys := make([]string, 0, len(r.adapters))
	for key := range r.adapters {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	items := make([]InstanceStatus, 0)
	for _, key := range keys {
		statuses, err := r.adapters[key].Reconcile()
		if err != nil {
			return nil, err
		}
		items = append(items, statuses...)
	}

	return items, nil
}

func (r *Registry) CreateTransportSession(req TransportSessionRequest) (TransportSession, error) {
	adapter, ok := r.Get(req.RuntimeClass)
	if !ok {
		return TransportSession{}, ErrAdapterNotFound
	}
	return adapter.CreateTransportSession(req)
}
