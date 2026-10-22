package clientapi

import (
	"encoding/json"
	"net/http"
	"time"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/pairing"
)

type issuePairingCodeRequest struct {
	TTLSeconds int `json:"ttlSeconds"`
}

// RegisterPairingAdminRoutes lets an authenticated admin mint a short-lived,
// single-use pairing code without physical access to the host. The route is
// on the client HTTPS surface so a remote admin (LAN/VPN) can reach it; it is
// still gated by a valid account bearer token and an active-admin check.
func RegisterPairingAdminRoutes(mux *http.ServeMux, svc *pairing.Service, authSvc *auth.Service) {
	if svc == nil || authSvc == nil {
		return
	}
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

	mux.Handle("POST /api/v1/admin/pairing-codes",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor := middleware.GetAuthenticatedUser(r.Context())
			user, err := authSvc.GetUser(r.Context(), actor.UserID)
			if err != nil || user.Type != "admin" || user.Status != "active" {
				middleware.WriteJSONError(w, http.StatusForbidden, "admin access required")
				return
			}
			var req issuePairingCodeRequest
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&req)
			}
			ttl := time.Duration(req.TTLSeconds) * time.Second
			code, expiresAt, err := svc.IssueCode(ttl)
			if err != nil {
				middleware.WriteJSONError(w, http.StatusInternalServerError, "could not issue pairing code")
				return
			}
			middleware.WriteJSON(w, http.StatusCreated, map[string]any{
				"code":      code,
				"expiresAt": expiresAt.UTC().Format(time.RFC3339),
			})
		}))))
}
