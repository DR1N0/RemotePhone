package main

import (
	"context"
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"time"

	"mako/internal/controlplane/admin"
	"mako/internal/controlplane/apiserver/adminapi"
	"mako/internal/controlplane/apiserver/clientapi"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/controller"
	"mako/internal/controlplane/image"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/mdns"
	"mako/internal/controlplane/pairing"
	"mako/internal/controlplane/persistence"
	"mako/internal/controlplane/placement"
	"mako/internal/controlplane/reconcile"
	"mako/internal/controlplane/serverinfo"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/tlsidentity"
	"mako/internal/controlplane/transport"
	"mako/internal/controlplane/workerproc"

	_ "modernc.org/sqlite"
)

func newAdminMux(svc *auth.Service, instanceSvc *instance.Service, adminSvc *admin.Service, transportSvc *transport.Service, imageSvc *image.Service) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	adminapi.RegisterAuthRoutes(mux, svc)
	adminapi.RegisterInstanceRoutes(mux, instanceSvc, adminSvc, svc)
	adminapi.RegisterAdminManagementRoutes(mux, adminSvc, svc, transportSvc)
	adminapi.RegisterAdminAuditRoutes(mux, adminSvc, svc)
	adminapi.RegisterImageRoutes(mux, imageSvc, svc)
	adminapi.RegisterOwnerRoutes(mux, svc)
	return mux
}

// newClientMux also mounts session/invite/signaling routes — session
// moderation has no cross-owner admin concern (see
// docs/plans/session-transport-redesign.md), so none of this is on
// newAdminMux.
func newClientMux(svc *auth.Service, instanceSvc *instance.Service, sessionSvc *session.Service, transportSvc *transport.Service, imageSvc *image.Service, pairingSvc *pairing.Service, serverInfo *serverinfo.Info) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	clientapi.RegisterAuthRoutes(mux, svc)
	clientapi.RegisterAccessCodeRoutes(mux, svc, pairingSvc, serverInfo)
	serverinfo.RegisterRoutes(mux, serverInfo)
	clientapi.RegisterInstanceRoutes(mux, instanceSvc, svc)
	clientapi.RegisterTemplateRoutes(mux, instanceSvc, svc)
	clientapi.RegisterSessionRoutes(mux, sessionSvc, transportSvc, svc)
	clientapi.RegisterSessionInviteRoutes(mux, sessionSvc, svc)
	clientapi.RegisterSignalingRoutes(mux, sessionSvc, instanceSvc, transportSvc, svc)
	clientapi.RegisterImageRoutes(mux, imageSvc, svc)
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

	// MAKO_WORKER_BINARY_PATH defaults to the sibling path of this running
	// binary — `go build ./cmd/...` normally drops both mako-controlplane
	// and mako-worker into the same directory. From here on, every running
	// instance gets its own mako-worker process, spawned on demand and
	// torn down with it — see docs/plans/worker-lifecycle.md. There is no
	// single shared worker/base URL anymore (MAKO_WORKER_BASE_URL, used
	// through Phase 3, is retired).
	//
	// os.Executable() (not os.Args[0]) because exec.Command requires a path
	// containing a separator to treat it as relative/absolute rather than a
	// $PATH lookup: launching this binary as "./mako-controlplane" makes
	// filepath.Dir(os.Args[0]) == ".", and Join(".", "mako-worker") collapses
	// to the bare string "mako-worker" — no separator — which exec.Command
	// then searches $PATH for instead of finding the sibling file.
	workerBinaryPath := envOrDefault("MAKO_WORKER_BINARY_PATH", "")
	if workerBinaryPath == "" {
		selfPath, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve own executable path: %w", err)
		}
		workerBinaryPath = filepath.Join(filepath.Dir(selfPath), workerBinaryName())
	}
	placementSelector := placement.NewSelector(workerproc.OSExecer{}, workerBinaryPath)
	instanceSvc := instance.NewService(db, placementSelector)
	imageSvc := image.NewService(db, placementSelector)
	instanceSvc.SetBaseImageResolver(imageSvc)

	// Matches the TS config's MAKO_OWNER_ABSENCE_TIMEOUT_MS default (5
	// minutes) — see the pre-Go TypeScript implementation's env config.
	ownerAbsenceTimeout := envOrDefaultDuration("MAKO_OWNER_ABSENCE_TIMEOUT_MS", 5*time.Minute)
	// Shared with transport.Service: cross-package request-time
	// serialization for the same attachment only works if both packages
	// lock the same map (see session.KeyedMutex's doc comment).
	keyedMutex := &session.KeyedMutex{}
	sessionSvc := session.NewService(db, svc, keyedMutex, ownerAbsenceTimeout)
	transportSvc := transport.NewService(db, sessionSvc, instanceSvc, keyedMutex)
	adminSvc := admin.NewService(db, svc, sessionSvc, instanceSvc)

	// workerProbe (keyed by instance ID — there is no separate worker
	// identity under the 1:1 model) health-checks each running instance's
	// worker and respawns+adopts on failure. sessionProbe is the
	// owner-absence reconciler. Neither triggers on its own: the resync
	// ticker below is what discovers candidates and enqueues them each
	// tick, since nothing else fires on a deadline merely elapsing or a
	// worker merely going quiet (unlike e.g. a disconnect, which a handler
	// could enqueue inline but doesn't need to).
	workerLivenessProbe := reconcile.NewWorkerLivenessProbe(instanceSvc, placementSelector)
	ownerAbsenceProbe := reconcile.NewOwnerAbsenceProbe(sessionSvc, transportSvc)
	ctrl := controller.New(workerLivenessProbe, ownerAbsenceProbe)
	go ctrl.Run(ctx)

	// Matches the TS config's MAKO_RECONCILE_INTERVAL_MS default (5s).
	reconcileInterval := envOrDefaultDuration("MAKO_RECONCILE_INTERVAL_MS", 5*time.Second)
	go runOwnerAbsenceResync(ctx, sessionSvc, ctrl, reconcileInterval)
	go runWorkerLivenessResync(ctx, instanceSvc, ctrl, reconcileInterval)

	// The client surface serves HTTPS with a self-signed certificate the
	// control plane generates on first run and reuses after — see
	// docs/plans/tls-identity.md. PIN-confirmed pairing / SPKI pinning
	// (the trust layer a real client would use to verify this certificate
	// instead of blind TOFU) is deliberately deferred until a native
	// client exists to pair with. The admin surface stays plain HTTP: its
	// loopback binding is its own, independent security barrier (see
	// docs/architecture/security-model.md), not something TLS changes.
	certPath := envOrDefault("MAKO_TLS_CERT_PATH", dbPath+".cert.pem")
	keyPath := envOrDefault("MAKO_TLS_KEY_PATH", dbPath+".key.pem")
	tlsCert, err := tlsidentity.NewIdentity(certPath, keyPath).GetOrCreate()
	if err != nil {
		return err
	}
	pairingSvc, err := pairing.NewFromEnv(tlsCert)
	if err != nil {
		return err
	}
	serverInfo, err := serverinfo.LoadOrCreate(
		envOrDefault("MAKO_SERVER_ID_PATH", dbPath+".server-id"),
		os.Getenv("MAKO_SERVER_NAME"),
	)
	if err != nil {
		return err
	}
	log.Printf("server identity: %s (%s)", serverInfo.Name, serverInfo.ServerID)
	if err := svc.SeedBootstrapAccessCode(ctx, pairingSvc.PIN(), 24*time.Hour); err != nil {
		return fmt.Errorf("seed bootstrap access code: %w", err)
	}
	log.Printf("bootstrap access code: %s (certificate SHA-256: %s)", pairingSvc.PIN(), pairingSvc.CertSHA256())

	adminSrv := &http.Server{Addr: "127.0.0.1:3001", Handler: newAdminMux(svc, instanceSvc, adminSvc, transportSvc, imageSvc)}
	clientAddr := envOrDefault("MAKO_CLIENT_ADDR", "127.0.0.1:3000")
	clientSrv := &http.Server{
		Addr:      clientAddr,
		Handler:   newClientMux(svc, instanceSvc, sessionSvc, transportSvc, imageSvc, pairingSvc, serverInfo),
		TLSConfig: &tls.Config{Certificates: []tls.Certificate{tlsCert}},
	}

	clientPort := 3000
	if _, portText, err := net.SplitHostPort(clientAddr); err == nil {
		if parsed, err := strconv.Atoi(portText); err == nil {
			clientPort = parsed
		}
	}
	advertiser, err := mdns.Start(serverInfo, clientPort, os.Getenv("MAKO_DISABLE_MDNS") == "1")
	if err != nil {
		log.Printf("mDNS discovery disabled: %v", err)
	} else if advertiser != nil {
		defer advertiser.Shutdown()
		log.Printf("mDNS: advertising _mako._tcp on port %d", clientPort)
	}

	errCh := make(chan error, 2)
	go func() { errCh <- adminSrv.ListenAndServe() }()
	go func() { errCh <- clientSrv.ListenAndServeTLS("", "") }() // cert/key already in TLSConfig

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

// runOwnerAbsenceResync periodically discovers sessions whose owner-absence
// deadline has passed and enqueues them on ctrl for reconciliation. This is
// candidate discovery only — ctrl.Run's dispatch to
// reconcile.OwnerAbsenceProbe.Reconcile re-verifies each session is still
// eligible before acting, so a session enqueued here and then resumed
// before its turn is simply skipped as a no-op.
func runOwnerAbsenceResync(ctx context.Context, sessionSvc *session.Service, ctrl *controller.Controller, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := sessionSvc.ListExpiredOwnerAbsenceSessionIDs(ctx, time.Now())
			if err != nil {
				log.Printf("owner-absence resync: list expired sessions: %v", err)
				continue
			}
			for _, id := range ids {
				ctrl.EnqueueSession(id)
			}
		}
	}
}

// runWorkerLivenessResync periodically discovers every instance whose
// desired state is "running" and enqueues it on ctrl for reconciliation.
// Candidate discovery only — ctrl.Run's dispatch to
// reconcile.WorkerLivenessProbe.Reconcile re-checks desired state and
// health before acting, so this never needs to be race-free against a
// concurrent stop.
func runWorkerLivenessResync(ctx context.Context, instanceSvc *instance.Service, ctrl *controller.Controller, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ids, err := instanceSvc.ListRunningInstanceIDs(ctx)
			if err != nil {
				log.Printf("worker-liveness resync: list running instances: %v", err)
				continue
			}
			for _, id := range ids {
				ctrl.EnqueueWorker(id)
			}
		}
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// workerBinaryName returns the sibling mako-worker executable's filename.
// Windows requires the .exe suffix when resolving the default path; callers
// that set MAKO_WORKER_BINARY_PATH explicitly are unaffected.
func workerBinaryName() string {
	if runtime.GOOS == "windows" {
		return "mako-worker.exe"
	}
	return "mako-worker"
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
