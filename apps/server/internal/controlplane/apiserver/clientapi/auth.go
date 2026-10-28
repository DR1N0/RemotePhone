// Package clientapi serves the LAN-reachable, PIN-paired client HTTP
// surface, strictly owner-scoped. This file wires its auth routes: login/
// me/logout (shared with adminapi via commonauth) plus the client-surface-
// only register/pairing-invite(retired)/credential-revoke routes.
package clientapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"remotephone/internal/controlplane/apiserver/commonauth"
	"remotephone/internal/controlplane/apiserver/middleware"
	"remotephone/internal/controlplane/auth"

	"github.com/google/uuid"
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

type registerRequest struct {
	InviteToken string `json:"inviteToken"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DeviceLabel string `json:"deviceLabel"`
}

// RegisterAuthRoutes mounts the client-surface auth routes on mux: it first
// delegates to commonauth.RegisterRoutes for login/me/logout (with
// sourceThrottle=false — a public-facing client surface sees every proxied
// client as the same address, so address-based throttling there would let a
// few failures from unrelated users block everyone; only per-username
// throttling applies here), then adds the client-only routes below.
func RegisterAuthRoutes(mux *http.ServeMux, svc *auth.Service) {
	commonauth.RegisterRoutes(mux, svc, false)

	mux.HandleFunc("POST /api/v1/auth/register", func(w http.ResponseWriter, r *http.Request) {
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid registration request")
			return
		}

		normalizedUsername := strings.ToLower(strings.TrimSpace(req.Username))
		deviceLabel := strings.TrimSpace(req.DeviceLabel)
		if req.InviteToken == "" ||
			!usernamePattern.MatchString(normalizedUsername) ||
			!isValidPassword(req.Password) ||
			deviceLabel == "" {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid registration request")
			return
		}

		requestID := uuid.NewString()
		user, token, err := svc.Register(r.Context(), req.InviteToken, normalizedUsername, req.Password, deviceLabel, requestID)
		if err != nil {
			if errors.Is(err, auth.ErrUsernameConflict) {
				middleware.WriteJSONError(w, http.StatusConflict, "username is already registered")
				return
			}
			// auth.ErrRegistrationInviteInvalid and every other failure mode
			// from Register collapse to the same generic 400 — this is
			// deliberate, matching the TS code's own lack of distinct status
			// codes for sub-cases here.
			middleware.WriteJSONError(w, http.StatusBadRequest, "registration failed")
			return
		}

		middleware.WriteJSON(w, http.StatusCreated, map[string]any{"user": user, "token": token})
	})

	mux.HandleFunc("POST /api/v1/auth/pairing-invites", func(w http.ResponseWriter, r *http.Request) {
		middleware.WriteJSONError(w, http.StatusGone, "pairing invitations retired")
	})

	mux.HandleFunc("POST /api/v1/auth/pairing-invites/redeem", func(w http.ResponseWriter, r *http.Request) {
		middleware.WriteJSONError(w, http.StatusGone, "pairing invitation redemption retired")
	})

	mux.Handle("POST /api/v1/auth/credentials/{id}/revoke",
		middleware.RequireAuth(svc)(middleware.RequireScope("account")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			id := r.PathValue("id")
			if err := svc.RevokeCredential(r.Context(), caller.UserID, id); err != nil {
				if errors.Is(err, auth.ErrCredentialNotFound) || errors.Is(err, auth.ErrCredentialNotOwned) {
					middleware.WriteJSONError(w, http.StatusNotFound, "credential not found")
					return
				}
				panic(err)
			}
			w.WriteHeader(http.StatusNoContent)
		}))),
	)
}
