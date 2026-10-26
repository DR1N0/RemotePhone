// Package admin implements admin-only cross-owner operations: user
// management (list/promote/demote/ban/unban/credential-revoke),
// cross-owner instance listing, audit-log retrieval, and revoking a guest's
// session attachment. Every method requires its caller to be an active
// admin, checked via auth.Service.RequireActiveAdmin — never re-derived
// here. Ported from the pre-Go TypeScript admin service; see
// docs/plans/admin-reconciliation.md for the port's design decisions.
//
// Deliberately does not depend on transport.Service: ban/guest-revoke do
// DB-only work in their own transaction and return the affected
// (sessionID, attachmentID) pairs still needing worker-side teardown — the
// adminapi HTTP handler drives that afterward, the same two-step pattern
// clientapi/sessions.go's disconnect handler already uses.
package admin
