// Package controller runs the control plane's background reconciliation
// loops. There is one Controller, dispatching to two Reconciler probes
// (worker-liveness, owner-absence) through a single shared workqueue —
// not two separate controllers — since both are convergence loops over
// shared state and splitting them would duplicate queue/resync/shutdown
// plumbing for no benefit.
package controller

import (
	"context"
	"strings"

	"k8s.io/client-go/util/workqueue"
)

// Reconciler converges the actual state of one resource (identified by id)
// toward its desired state. Implementations must be idempotent: Reconcile
// may be called more than once for the same id, including after a prior
// call already succeeded.
type Reconciler interface {
	Reconcile(ctx context.Context, id string) error
}

// Controller dispatches queued keys to the probe registered for their
// prefix ("worker:" or "session:").
type Controller struct {
	queue        workqueue.RateLimitingInterface
	workerProbe  Reconciler
	sessionProbe Reconciler
}

func New(workerProbe, sessionProbe Reconciler) *Controller {
	return &Controller{
		queue:        workqueue.NewRateLimitingQueue(workqueue.DefaultControllerRateLimiter()),
		workerProbe:  workerProbe,
		sessionProbe: sessionProbe,
	}
}

func (c *Controller) EnqueueWorker(id string)  { c.queue.Add("worker:" + id) }
func (c *Controller) EnqueueSession(id string) { c.queue.Add("session:" + id) }

// Run processes queued keys until ctx is canceled.
func (c *Controller) Run(ctx context.Context) {
	go func() {
		<-ctx.Done()
		c.queue.ShutDown()
	}()
	for c.processNext(ctx) {
	}
}

func (c *Controller) processNext(ctx context.Context) bool {
	item, shutdown := c.queue.Get()
	if shutdown {
		return false
	}
	defer c.queue.Done(item)

	key := item.(string)
	if err := c.dispatch(ctx, key); err != nil {
		c.queue.AddRateLimited(item)
		return true
	}
	c.queue.Forget(item)
	return true
}

func (c *Controller) dispatch(ctx context.Context, key string) error {
	switch {
	case strings.HasPrefix(key, "worker:"):
		return c.workerProbe.Reconcile(ctx, strings.TrimPrefix(key, "worker:"))
	case strings.HasPrefix(key, "session:"):
		return c.sessionProbe.Reconcile(ctx, strings.TrimPrefix(key, "session:"))
	default:
		return nil
	}
}
