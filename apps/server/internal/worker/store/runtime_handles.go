package store

import "mako/internal/worker/runtime"

type RuntimeHandles = runtime.MemoryHandleStore

func NewRuntimeHandles() *RuntimeHandles {
	return runtime.NewMemoryHandleStore()
}
