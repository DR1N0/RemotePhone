package placement

import "fmt"

// ErrUnsupported is returned when a Policy names a kind Selector can't
// honor.
var ErrUnsupported = fmt.Errorf("placement policy not supported")

// Policy is the control-plane request-level placement policy a
// CreateInstanceRequest carries — the worker itself never sees this.
type Policy struct {
	Kind     string `json:"kind"`
	WorkerID string `json:"workerId,omitempty"`
}

// Selector validates that a requested placement policy can actually be
// honored. Deliberately trivial for this phase: the control plane talks to
// exactly one worker (a single shared client, configured with one base URL
// at construction) — there's no pool to select among, so Selector's only
// job is rejecting policy kinds this phase can't satisfy, matching the TS
// placement-service.ts's own "local-only, or reject" behavior. See
// docs/architecture/decisions-log.md (2026-10-14): the 1:1 ephemeral
// worker-per-instance model has no placement/affinity problem to solve.
// Kept as its own package (not inlined into instance) so a later phase can
// give it real per-instance worker-selection logic without changing
// callers — this is the "placement/" package named in the original Phase 1
// skeleton, not a bespoke replacement for it.
type Selector struct{}

func NewSelector() *Selector { return &Selector{} }

// Validate reports ErrUnsupported unless policy's kind is "local-only" —
// the only kind any phase before a real worker pool exists can honor.
func (s *Selector) Validate(policy Policy) error {
	if policy.Kind != "local-only" {
		return fmt.Errorf("%w: %q", ErrUnsupported, policy.Kind)
	}
	return nil
}
