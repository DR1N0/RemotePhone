// Package middleware provides shared bearer-auth HTTP middleware for both
// apiserver surfaces. RequireAuth authenticates a bearer token against
// auth.Service and stores the resulting *auth.AuthenticatedUser on the
// request context; RequireScope, chained after it, enforces that the
// authenticated credential's scope matches what the route requires. The
// two are deliberately separate layers so an invalid/missing credential
// fails with 401 while a valid-but-wrong-scope credential fails with 403.
package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"

	"mako/internal/controlplane/auth"
)

const bearerPrefix = "Bearer "

// contextKey is an unexported type so values this package stores on a
// request context can never collide with a key set by another package
// using a plain string.
type contextKey int

const authenticatedUserKey contextKey = iota

// RequireAuth returns middleware that authenticates the request's bearer
// token against svc and, on success, stores the resulting
// *auth.AuthenticatedUser on the request context for downstream handlers
// (retrieve it with GetAuthenticatedUser). A missing/malformed
// Authorization header, or a token svc.Authenticate does not recognize as
// valid, is rejected with 401 before the wrapped handler ever runs.
func RequireAuth(svc *auth.Service) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var token string
			if header := r.Header.Get("Authorization"); strings.HasPrefix(header, bearerPrefix) {
				token = strings.TrimPrefix(header, bearerPrefix)
			}
			if token == "" {
				WriteJSONError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}

			// A non-nil err here is an infrastructure failure (e.g. a DB
			// error), distinct from an ordinary invalid/revoked credential
			// (which Authenticate reports as (nil, nil) — see its doc
			// comment). Collapsing both into the same 401 would mislead
			// on-call engineers into reading a DB outage as routine bad
			// auth, so it panics instead, consistent with this codebase's
			// established convention of panicking on unmapped/unexpected
			// service errors and letting net/http's per-request panic
			// recovery turn it into a 500 for just this one connection.
			user, err := svc.Authenticate(r.Context(), token)
			if err != nil {
				panic(fmt.Errorf("authenticate: %w", err))
			}
			if user == nil {
				WriteJSONError(w, http.StatusUnauthorized, "invalid or revoked credential")
				return
			}

			ctx := context.WithValue(r.Context(), authenticatedUserKey, user)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope returns middleware, chained after RequireAuth, that rejects
// a request with 403 unless the authenticated credential's scope matches
// scope exactly. It is a separate layer from RequireAuth on purpose: a
// valid credential with the wrong scope is a 403 (forbidden), never a 401
// (unauthorized).
func RequireScope(scope string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := GetAuthenticatedUser(r.Context())
			if user.Scope != scope {
				WriteJSONError(w, http.StatusForbidden, "credential scope is not authorized")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// GetAuthenticatedUser returns the *auth.AuthenticatedUser stored on ctx by
// RequireAuth. Calling it outside RequireAuth's chain is a programming
// error, not a request-time condition, so it panics rather than returning
// a zero value that would quietly mask a wiring bug.
func GetAuthenticatedUser(ctx context.Context) *auth.AuthenticatedUser {
	user, ok := ctx.Value(authenticatedUserKey).(*auth.AuthenticatedUser)
	if !ok {
		panic("middleware.GetAuthenticatedUser called without RequireAuth in the handler chain")
	}
	return user
}

// IsLoopbackAddress reports whether addr is exactly one of the loopback
// address literals Go's net package produces: "127.0.0.1", "::1", or
// "::ffff:127.0.0.1". It is an exact string match, never CIDR/prefix
// matching, so "127.0.0.2" is not loopback.
func IsLoopbackAddress(addr string) bool {
	switch addr {
	case "127.0.0.1", "::1", "::ffff:127.0.0.1":
		return true
	default:
		return false
	}
}

// ClientAddress returns the real TCP peer address for r — Go's own
// net/http representation of who actually opened the socket, never a
// value derived from a client-supplied header such as X-Forwarded-For.
// r.RemoteAddr is in "host:port" form; this strips the port and returns
// just the host. If the address cannot be split, it returns "" rather
// than guessing, so a resolution failure is never mistaken for loopback.
func ClientAddress(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return ""
	}
	return host
}
