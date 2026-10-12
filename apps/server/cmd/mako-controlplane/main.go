package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/apiserver/clientapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/persistence"

	_ "modernc.org/sqlite"
)

func newAdminMux(svc *auth.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	adminapi.RegisterAuthRoutes(mux, svc)
	return mux
}

func newClientMux(svc *auth.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	clientapi.RegisterAuthRoutes(mux, svc)
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

	dbPath := envOrDefault("MAKO_DB_PATH", "mako.db")
	bootstrapSecretPath := envOrDefault("MAKO_BOOTSTRAP_SECRET_PATH", dbPath+".bootstrap-secret")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := persistence.ApplyMigrations(db); err != nil {
		return err
	}
	svc, err := auth.NewService(db, bootstrapSecretPath)
	if err != nil {
		return err
	}

	adminSrv := &http.Server{Addr: "127.0.0.1:3001", Handler: newAdminMux(svc)}
	clientSrv := &http.Server{Addr: "127.0.0.1:3000", Handler: newClientMux(svc)}

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

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
