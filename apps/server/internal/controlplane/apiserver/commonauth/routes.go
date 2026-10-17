// Package commonauth registers the login/me/logout routes shared by both
// the admin and client HTTP surfaces. It exists only because adminapi and
// clientapi are sibling packages that must never import each other, yet
// both need the exact same login/me/logout handlers (with different login
// throttle behavior) — this small third package is the one place those
// handlers live, so neither surface carries its own copy.
package commonauth

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"
)

// usernamePattern mirrors auth.Service's own (unexported) normalized-username
// pattern: lowercase letters, digits, and underscores, 3-32 characters.
var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// isValidPassword reports whether password's byte length falls within
// [8, 1024], matching the HTTP-layer validation the TS route schemas
// perform before ever calling into AuthService.
func isValidPassword(password string) bool {
	return len(password) >= 8 && len(password) <= 1024
}

type loginRequest struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DeviceLabel string `json:"deviceLabel"`
}

// RegisterRoutes mounts POST /api/v1/auth/login, GET /api/v1/auth/me, and
// POST /api/v1/auth/logout on mux. sourceThrottle controls whether the login
// throttle also tracks a key derived from the caller's address (true on the
// admin surface, which sits behind a single local/LAN-facing proxy; false on
// the client surface, where every public client can share one peer address
// behind a TLS-terminating proxy, so address-based throttling there would
// throttle unrelated users together).
func RegisterRoutes(mux *http.ServeMux, svc *auth.Service, sourceThrottle bool) {
	throttle := newLoginThrottle()
	requireAccount := func(h http.HandlerFunc) http.Handler {
		return middleware.RequireAuth(svc)(middleware.RequireScope("account")(h))
	}

	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid login request")
			return
		}

		normalizedUsername := strings.ToLower(strings.TrimSpace(req.Username))
		deviceLabel := strings.TrimSpace(req.DeviceLabel)
		if !usernamePattern.MatchString(normalizedUsername) || !isValidPassword(req.Password) || deviceLabel == "" {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid login request")
			return
		}

		now := time.Now()
		usernameKey := "user:" + normalizedUsername
		var addrKey string
		// The direct peer is never derived from forwarded headers (see
		// middleware.ClientAddress). An unresolvable address disables
		// address-based throttling for this request entirely — it does NOT
		// fall back to throttling by some other value, and it never
		// disables the always-checked username key.
		if sourceThrottle {
			if addr := middleware.ClientAddress(r); addr != "" {
				addrKey = "addr:" + addr
			}
		}

		if throttle.tooManyFailures(usernameKey, now) || (addrKey != "" && throttle.tooManyFailures(addrKey, now)) {
			middleware.WriteJSONError(w, http.StatusTooManyRequests, "too many login attempts")
			return
		}

		user, token, err := svc.Login(r.Context(), auth.LoginInput{
			Username:    normalizedUsername,
			Password:    req.Password,
			DeviceLabel: deviceLabel,
		})
		if err != nil {
			// A throttled attempt above never reaches here, so it is never
			// itself counted as a new failure — only a genuine Login
			// rejection is recorded.
			throttle.recordFailure(usernameKey, now)
			if addrKey != "" {
				throttle.recordFailure(addrKey, now)
			}
			middleware.WriteJSONError(w, http.StatusUnauthorized, "invalid username or password")
			return
		}

		middleware.WriteJSON(w, http.StatusOK, map[string]any{"user": user, "token": token})
	})

	mux.Handle("GET /api/v1/auth/me", requireAccount(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		// Always a fresh read, never anything cached from the token, so a
		// status/type change made elsewhere is reflected immediately.
		user, err := svc.GetUser(r.Context(), caller.UserID)
		if err != nil {
			// The credential was just validated by RequireAuth against this
			// same user row, so GetUser failing here is an unexpected
			// infrastructure condition, not a normal request-time path.
			panic(err)
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"user": user})
	}))

	mux.Handle("POST /api/v1/auth/logout", requireAccount(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		if err := svc.RevokeCurrentCredential(r.Context(), caller.CredentialID); err != nil {
			panic(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}
