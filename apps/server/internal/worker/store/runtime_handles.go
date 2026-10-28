package store

import "remotephone/internal/worker/runtime"

type RuntimeHandles = runtime.MemoryHandleStore

func NewRuntimeHandles() *RuntimeHandles {
	return runtime.NewMemoryHandleStore()
}
