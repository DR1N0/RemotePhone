// Package transport ports mako's TypeScript TransportService and
// SignalingService (apps/controlplane/src/services/transport-service.ts,
// signaling-service.ts) to Go: establishing/tearing down an attachment's
// worker-side WebRTC transport, relaying SDP offers/ICE candidates, and —
// the one real redesign in this phase — owning attachment role transitions
// end to end via TransitionAttachmentRole, replacing TS's four-times-patched,
// differently-ordered promote/demote route handlers. See
// docs/plans/session-transport-redesign.md.
//
// This package depends on both session and instance (not the reverse) — it
// combines a session-package DB mutation with an instance-package worker
// call for the same attachment, which is why the orchestration lives here
// rather than on session.Service.
package transport
