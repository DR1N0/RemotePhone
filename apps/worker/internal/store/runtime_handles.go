package store

import "mako-worker/internal/runtime"

type RuntimeHandles = runtime.MemoryHandleStore

func NewRuntimeHandles() *RuntimeHandles {
	return runtime.NewMemoryHandleStore()
}
