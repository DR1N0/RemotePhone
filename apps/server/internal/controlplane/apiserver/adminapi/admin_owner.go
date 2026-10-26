package adminapi

import (
	"encoding/json"
	"net/http"
	"time"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/pairing"
	"mako/internal/controlplane/serverinfo"
)

type ownerAccessCodeRequest struct {
	Username   string `json:"username"`
	TTLSeconds int    `json:"ttlSeconds"`
}

type ownerResetPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// RegisterOwnerRoutes mounts the loopback owner console operations under the
// same /api/v1/admin namespace as the rest of the admin API. With no bearer
// token the middleware attributes the request to _SYSTEM; a bearer token is
// still accepted for programmatic clients.
func RegisterOwnerRoutes(mux *http.ServeMux, authSvc *auth.Service, pairingSvc *pairing.Service, info *serverinfo.Info) {
	if authSvc == nil {
		return
	}
	requireOwner := middleware.RequireOwner(authSvc)

	mux.Handle("POST /api/v1/admin/access-codes", requireOwner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ownerAccessCodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid owner request")
			return
		}
		var target *string
		if req.Username != "" {
			user, err := authSvc.GetUserByUsername(r.Context(), req.Username)
			if err != nil {
				middleware.WriteJSONError(w, http.StatusNotFound, "user not found")
				return
			}
			target = &user.ID
		}
		ttl := time.Duration(req.TTLSeconds) * time.Second
		if ttl <= 0 {
			ttl = 24 * time.Hour
		}
		system := auth.SystemUserID
		code, expiresAt, err := authSvc.IssueAccessCode(r.Context(), target, &system, ttl)
		if err != nil {
			middleware.WriteJSONError(w, http.StatusInternalServerError, "could not create access code")
			return
		}
		middleware.WriteJSON(w, http.StatusCreated, map[string]any{
			"code":      code,
			"expiresAt": expiresAt.UTC().Format(time.RFC3339),
			"target":    req.Username,
		})
	})))

	mux.Handle("POST /api/v1/admin/users/reset-password", requireOwner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req ownerResetPasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid owner request")
			return
		}
		if err := authSvc.ResetPassword(r.Context(), req.Username, req.Password); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]string{"status": "password reset"})
	})))

	mux.Handle("GET /api/v1/admin/server", requireOwner(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := map[string]any{}
		if info != nil {
			response["serverId"] = info.ServerID
			response["serverName"] = info.Name
		}
		if pairingSvc != nil {
			response["certSha256"] = pairingSvc.CertSHA256()
		}
		middleware.WriteJSON(w, http.StatusOK, response)
	})))
}
