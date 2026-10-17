// Package adminapi serves the loopback-bound admin HTTP surface: webui
// static assets, user/credential/audit management, cross-owner instance
// operations, and base-image promote/rollback. This file wires its auth
// routes: login/me/logout (shared with clientapi via commonauth) plus the
// admin-surface-only status/setup/registration-invites routes.
package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"mako/internal/controlplane/apiserver/commonauth"
	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"

	"github.com/google/uuid"
)

// usernamePattern mirrors auth.Service's own (unexported) normalized-username
// pattern.
var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// isValidPassword reports whether password's byte length falls within
// [8, 1024], matching the HTTP-layer validation the TS route schemas
// perform before ever calling into AuthService.
func isValidPassword(password string) bool {
	return len(password) >= 8 && len(password) <= 1024
}

// localhostHostPattern, loopbackIPv4HostPattern, and loopbackIPv6HostPattern
// mirror the TS route module's hasLocalSetupHost: the Host header (optional
// ":port" suffix) must denote localhost, 127.0.0.1, or [::1] literally — no
// wildcard/subdomain matching.
var (
	localhostHostPattern    = regexp.MustCompile(`(?i)^localhost(:\d+)?$`)
	loopbackIPv4HostPattern = regexp.MustCompile(`^127\.0\.0\.1(:\d+)?$`)
	loopbackIPv6HostPattern = regexp.MustCompile(`(?i)^\[::1\](:\d+)?$`)
)

func isLocalSetupHost(host string) bool {
	if host == "" {
		return false
	}
	return localhostHostPattern.MatchString(host) ||
		loopbackIPv4HostPattern.MatchString(host) ||
		loopbackIPv6HostPattern.MatchString(host)
}

type setupRequest struct {
	Username        string `json:"username"`
	Password        string `json:"password"`
	DeviceLabel     string `json:"deviceLabel"`
	BootstrapSecret string `json:"bootstrapSecret"`
}

// RegisterAuthRoutes mounts the admin-surface auth routes on mux: it first
// delegates to commonauth.RegisterRoutes for login/me/logout (with
// sourceThrottle=true, since the admin surface sits behind a single
// local/LAN-facing proxy where address-based throttling is meaningful), then
// adds the three admin-only routes below.
func RegisterAuthRoutes(mux *http.ServeMux, svc *auth.Service) {
	commonauth.RegisterRoutes(mux, svc, true)

	mux.HandleFunc("GET /api/v1/auth/status", func(w http.ResponseWriter, r *http.Request) {
		claimed, err := svc.GetSetupStatus(r.Context())
		if err != nil {
			panic(err)
		}
		// Never include anything about the bootstrap secret in this
		// response — it reports only whether the server has been claimed.
		middleware.WriteJSON(w, http.StatusOK, map[string]bool{"claimed": claimed})
	})

	mux.HandleFunc("POST /api/v1/auth/setup", func(w http.ResponseWriter, r *http.Request) {
		// The local-only gate is checked BEFORE any JSON decoding: an
		// unauthorized caller's request body is never even parsed.
		host := r.Host
		if host == "" {
			host = r.URL.Host
		}
		if !middleware.IsLoopbackAddress(middleware.ClientAddress(r)) || !isLocalSetupHost(host) {
			middleware.WriteJSONError(w, http.StatusForbidden, "first-admin setup must be performed locally")
			return
		}

		var req setupRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid setup request")
			return
		}

		// The username is normalized (trim + lowercase) the same way every
		// other username-accepting endpoint (/login, /register) does, before
		// validating it against usernamePattern.
		normalizedUsername := strings.ToLower(strings.TrimSpace(req.Username))
		deviceLabel := strings.TrimSpace(req.DeviceLabel)
		if !usernamePattern.MatchString(normalizedUsername) ||
			!isValidPassword(req.Password) ||
			deviceLabel == "" ||
			req.BootstrapSecret == "" || len(req.BootstrapSecret) > 256 {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid setup request")
			return
		}

		user, token, err := svc.SetupAdmin(r.Context(), auth.SetupAdminInput{
			Username:        normalizedUsername,
			Password:        req.Password,
			DeviceLabel:     deviceLabel,
			BootstrapSecret: req.BootstrapSecret,
		})
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrBootstrapSecretInvalid):
				middleware.WriteJSONError(w, http.StatusForbidden, "invalid bootstrap secret")
			case errors.Is(err, auth.ErrServerAlreadyClaimed):
				middleware.WriteJSONError(w, http.StatusConflict, "server has already been claimed")
			default:
				// Includes auth.ErrBootstrapSecretUnavailable: an
				// operator-misconfiguration condition, not a normal
				// request-time path, matching the TS code's own lack of an
				// explicit mapping for this case.
				panic(err)
			}
			return
		}

		middleware.WriteJSON(w, http.StatusCreated, map[string]any{"user": user, "token": token})
	})

	mux.Handle("POST /api/v1/auth/registration-invites",
		middleware.RequireAuth(svc)(middleware.RequireScope("account")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			requestID := uuid.NewString()
			token, expiresAt, err := svc.CreateRegistrationInvite(r.Context(), caller.UserID, &caller.CredentialID, requestID)
			if err != nil {
				if errors.Is(err, auth.ErrNotAdmin) {
					middleware.WriteJSONError(w, http.StatusForbidden, "only an active admin may issue registration invitations")
					return
				}
				panic(err)
			}

			middleware.WriteJSON(w, http.StatusCreated, map[string]any{"token": token, "expiresAt": expiresAt})
		}))),
	)
}
