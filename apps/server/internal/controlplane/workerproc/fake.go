package workerproc

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
)

// FakeExecer is a test double for Execer: instead of starting a real
// mako-worker binary, it starts an in-process HTTP server bound to exactly
// the address Spawn's env told it to use (parsed back out of
// MAKO_WORKER_LISTEN_ADDR), so the rest of the Spawn/WaitHealthy flow works
// completely unmodified against it. Exported (not test-only) so
// placement's and reconcile's own tests can reuse it instead of
// reimplementing a fake worker process — see
// docs/architecture/decisions-log.md's testing bar for this phase.
type FakeExecer struct {
	// Handler builds the HTTP handler a spawned fake "worker" should serve.
	// Called once per Start — a func rather than a fixed http.Handler so
	// tests simulating a crash-and-respawn can hand each spawn distinct,
	// independently toggleable state. Defaults to a handler that always
	// answers 200 on GET /healthz if nil.
	Handler func() http.Handler

	mu        sync.Mutex
	nextPID   int
	Starts    []FakeSpawn // one entry per Start call, in order, for assertions
	Processes []Process   // the Process returned by each Start call, same order as Starts
}

// FakeSpawn records one Start invocation's env, so tests can assert on
// which instance ID / port a spawn was for.
type FakeSpawn struct {
	Env []string
}

func (f *FakeExecer) Start(ctx context.Context, binaryPath string, env []string) (Process, error) {
	addr := envValue(env, "MAKO_WORKER_LISTEN_ADDR")
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}

	handler := f.Handler
	if handler == nil {
		handler = defaultFakeHandler
	}

	f.mu.Lock()
	if f.nextPID == 0 {
		// Start well above any real PID range so a test calling
		// workerproc.FromPID/Kill on this fake PID (e.g. exercising
		// Selector.ReleaseWorker end to end) always lands on "no such
		// process" rather than accidentally signaling a real one.
		f.nextPID = 900000
	}
	f.nextPID++
	pid := f.nextPID
	f.Starts = append(f.Starts, FakeSpawn{Env: env})
	f.mu.Unlock()

	server := &http.Server{Handler: handler()}
	go server.Serve(listener) //nolint:errcheck // closed via Kill, error is expected then

	proc := &fakeProcess{pid: pid, server: server, listener: listener}
	f.mu.Lock()
	f.Processes = append(f.Processes, proc)
	f.mu.Unlock()
	return proc, nil
}

func defaultFakeHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

type fakeProcess struct {
	pid      int
	server   *http.Server
	listener net.Listener
	mu       sync.Mutex
	killed   bool
}

func (p *fakeProcess) PID() int { return p.pid }

// Kill closes the fake worker's listener, simulating the worker process
// dying — any in-flight or future request to its base URL now fails,
// exactly like a real crashed process.
func (p *fakeProcess) Kill() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.killed {
		return nil
	}
	p.killed = true
	return p.server.Close()
}

func (p *fakeProcess) Wait() error { return nil }
