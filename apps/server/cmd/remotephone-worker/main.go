package main

import (
	"log"
	"net/http"
	stdruntime "runtime"

	workerapi "remotephone/internal/worker/api"
	"remotephone/internal/worker/config"
	"remotephone/internal/worker/host"
	workerruntime "remotephone/internal/worker/runtime"
	"remotephone/internal/worker/runtime/androidstudio"
	"remotephone/internal/worker/store"
	"remotephone/internal/worker/transport"
)

func main() {
	cfg := config.Load()

	handles := store.NewRuntimeHandles()
	transportStore := transport.NewStore()
	registry := buildStartupRuntimeRegistry(cfg, handles, transportStore)

	router := workerapi.NewRouter(workerapi.Dependencies{
		WorkerID:      cfg.WorkerID,
		ListenBaseURL: cfg.ListenBaseURL,
		HostInventory: workerapi.HostInventory{
			HostOS: hostOS(),
			Arch:   runtime2GOARCH(),
		},
		RuntimeRegistry: registry,
		TransportStore:  transportStore,
	})

	log.Printf("remotephone-worker listening on %s", cfg.ListenAddr)
	if err := http.ListenAndServe(cfg.ListenAddr, router); err != nil {
		log.Fatal(err)
	}
}

func hostOS() string {
	return host.NormalizeGOOS(stdruntime.GOOS)
}

func runtime2GOARCH() string {
	return stdruntime.GOARCH
}

func buildStartupRuntimeRegistry(cfg config.Config, handles workerruntime.HandleStore, transportStore *transport.Store) *workerruntime.Registry {
	registry := newRuntimeRegistry(handles)
	androidStudioAdapter := androidstudio.NewAdapter(cfg, handles, transportStore)
	if err := androidStudioAdapter.IsAvailable(); err == nil {
		if err := registry.Register(androidStudioAdapter); err != nil {
			log.Printf("skip android studio adapter registration: %v", err)
		}
	} else {
		log.Printf("android studio emulator unavailable: %v", err)
	}
	return registry
}

func newRuntimeRegistry(handles workerruntime.HandleStore, adapters ...workerruntime.Adapter) *workerruntime.Registry {
	return workerruntime.NewRegistryWithHandles(handles, adapters...)
}
