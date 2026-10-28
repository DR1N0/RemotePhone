// Package session ports remotephone's pre-Go TypeScript SessionService and
// SessionInviteService to Go: session/attachment lifecycle and
// invites. It deliberately does NOT own role-transition orchestration
// against the worker (transport.Service does, see
// docs/plans/session-transport-redesign.md) — this package exposes two
// low-level, transaction-participating primitives
// (SetAttachmentPendingTargetRole, ConfirmAttachmentRole) that transport
// calls as plain steps inside its own transactions.
package session
