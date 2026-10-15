package main

import (
	"context"
	"database/sql"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/apiserver/clientapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"

	_ "modernc.org/sqlite"
)

func newAdminMux(svc *auth.Service, instanceSvc *instance.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	adminapi.RegisterAuthRoutes(mux, svc)
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, svc)
	return mux
}

// newClientMux also mounts session/invite/signaling routes — session
// moderation has no cross-owner admin concern (see
// docs/plans/session-transport-redesign.md), so none of this is on
// newAdminMux.
func newClientMux(svc *auth.Service, instanceSvc *instance.Service, sessionSvc *session.Service, transportSvc *transport.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	clientapi.RegisterAuthRoutes(mux, svc)
	clientapi.RegisterInstanceRoutes(mux, instanceSvc, svc)
	clientapi.RegisterSessionRoutes(mux, sessionSvc, transportSvc, svc)
	clientapi.RegisterSessionInviteRoutes(mux, sessionSvc, svc)
	clientapi.RegisterSignalingRoutes(mux, sessionSvc, instanceSvc, transportSvc, svc)
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

	// MAKO_WORKER_BASE_URL matches the worker's own default listen address
	// (internal/worker/config.Load's MAKO_WORKER_BASE_URL default) — this
	// phase talks to exactly one externally-started worker; spawning it is
	// Phase 4's job (see docs/plans/roadmap.md).
	workerBaseURL := envOrDefault("MAKO_WORKER_BASE_URL", "http://127.0.0.1:7780")
	workerClient := instance.NewHTTPWorkerClient(workerBaseURL)
	instanceSvc := instance.NewService(db, workerClient, placement.NewSelector())

	// Matches the TS config's MAKO_OWNER_ABSENCE_TIMEOUT_MS default (5
	// minutes) — see apps/controlplane/src/config/env.ts.
	ownerAbsenceTimeout := envOrDefaultDuration("MAKO_OWNER_ABSENCE_TIMEOUT_MS", 5*time.Minute)
	// Shared with transport.Service: cross-package request-time
	// serialization for the same attachment only works if both packages
	// lock the same map (see session.KeyedMutex's doc comment).
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, svc, keyedMutex, ownerAbsenceTimeout)
	transportSvc := transport.NewService(db, sessionSvc, instanceSvc, workerClient, keyedMutex)

	adminSrv := &http.Server{Addr: "127.0.0.1:3001", Handler: newAdminMux(svc, instanceSvc)}
	clientSrv := &http.Server{Addr: "127.0.0.1:3000", Handler: newClientMux(svc, instanceSvc, sessionSvc, transportSvc)}

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

// envOrDefaultDuration parses key as a positive integer count of
// milliseconds (matching the TS config's *_MS env var convention), or
// returns fallback if key is unset. An invalid (non-integer or
// non-positive) value is a startup configuration error, not something to
// silently fall back from.
func envOrDefaultDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	ms, err := strconv.ParseInt(v, 10, 64)
	if err != nil || ms <= 0 {
		log.Fatalf("invalid %s value %q: must be a positive integer (milliseconds)", key, v)
	}
	return time.Duration(ms) * time.Millisecond
}
