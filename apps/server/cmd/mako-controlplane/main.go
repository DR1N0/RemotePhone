package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"
)

func newAdminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func newClientMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	adminSrv := &http.Server{Addr: "127.0.0.1:3001", Handler: newAdminMux()}
	clientSrv := &http.Server{Addr: "127.0.0.1:3000", Handler: newClientMux()}

	errCh := make(chan error, 2)
	go func() { errCh <- adminSrv.ListenAndServe() }()
	go func() { errCh <- clientSrv.ListenAndServe() }()

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	adminSrv.Shutdown(shutdownCtx)
	clientSrv.Shutdown(shutdownCtx)
	return nil
}
