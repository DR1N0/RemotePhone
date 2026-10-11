package controller

import (
	"context"
	"sync"
	"testing"
	"time"
)

type fakeReconciler struct {
	mu   sync.Mutex
	seen []string
}

func (f *fakeReconciler) Reconcile(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, id)
	return nil
}

func (f *fakeReconciler) ids() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.seen...)
}

func TestControllerDispatchesByPrefix(t *testing.T) {
	workerProbe := &fakeReconciler{}
	sessionProbe := &fakeReconciler{}
	c := New(workerProbe, sessionProbe)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	c.EnqueueWorker("w1")
	c.EnqueueSession("s1")

	deadline := time.After(time.Second)
	for len(workerProbe.ids()) != 1 || len(sessionProbe.ids()) != 1 {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for dispatch: worker=%v session=%v", workerProbe.ids(), sessionProbe.ids())
		case <-time.After(10 * time.Millisecond):
		}
	}

	if got := workerProbe.ids(); len(got) != 1 || got[0] != "w1" {
		t.Errorf("workerProbe.ids() = %v, want [w1]", got)
	}
	if got := sessionProbe.ids(); len(got) != 1 || got[0] != "s1" {
		t.Errorf("sessionProbe.ids() = %v, want [s1]", got)
	}
}
