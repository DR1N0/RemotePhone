// Package reconcile holds controller.Reconciler implementations — the
// actual convergence logic the generic internal/controlplane/controller
// package dispatches to. Currently just the owner-absence probe; the
// worker-liveness probe (roadmap Phase 4) will live here too once real
// exec.Command worker spawning gives it something to health-check.
package reconcile

import (
	"context"
	"fmt"
	"time"

	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"

	"github.com/google/uuid"
)

// OwnerAbsenceProbe implements controller.Reconciler (as the sessionProbe
// passed to controller.New) for sessions whose owner has been gone past
// their owner-absence deadline. Reconcile(ctx, sessionID) is always safe to
// call speculatively — session.Service.CloseExpiredOwnerAbsenceSession
// re-verifies the deadline inside its own transaction and is a no-op if the
// session already isn't eligible (closed already, or the owner resumed and
// cleared the deadline).
type OwnerAbsenceProbe struct {
	sessionSvc   *session.Service
	transportSvc *transport.Service
	now          func() time.Time
}

func NewOwnerAbsenceProbe(sessionSvc *session.Service, transportSvc *transport.Service) *OwnerAbsenceProbe {
	return &OwnerAbsenceProbe{sessionSvc: sessionSvc, transportSvc: transportSvc, now: time.Now}
}

// Reconcile closes sessionID if its owner-absence deadline has passed, then
// drives worker-side transport teardown for every attachment the close
// affected. Audit events this produces have a nil actor — system-triggered,
// not an authenticated user's action.
func (p *OwnerAbsenceProbe) Reconcile(ctx context.Context, sessionID string) error {
	actor := audit.NewContext(nil, nil, uuid.NewString())
	_, attachmentIDs, closed, err := p.sessionSvc.CloseExpiredOwnerAbsenceSession(ctx, sessionID, p.now(), actor)
	if err != nil {
		return fmt.Errorf("close expired owner absence session: %w", err)
	}
	if !closed {
		return nil
	}
	for _, attachmentID := range attachmentIDs {
		if _, err := p.transportSvc.TeardownPendingAttachment(ctx, sessionID, attachmentID, actor); err != nil {
			return fmt.Errorf("teardown pending attachment %s: %w", attachmentID, err)
		}
	}
	return nil
}
