package clientapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/pairing"
	"mako/internal/controlplane/serverinfo"
)

type createAccessCodeRequest struct {
	TTLSeconds int `json:"ttlSeconds"`
}

type redeemAccessCodeRequest struct {
	Code        string `json:"code"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DeviceLabel string `json:"deviceLabel"`
}

// RegisterAccessCodeRoutes mounts the client-available code operations.
// Creating a code is authenticated; redeeming one is public because it is the
// first step on a device that has no credential yet.
func RegisterAccessCodeRoutes(mux *http.ServeMux, authSvc *auth.Service, pairingSvc *pairing.Service, serverInfo *serverinfo.Info) {
	if authSvc == nil {
		return
	}
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

	mux.Handle("POST /api/v1/access-codes",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			actor := middleware.GetAuthenticatedUser(r.Context())
			user, err := authSvc.GetUser(r.Context(), actor.UserID)
			if err != nil {
				middleware.WriteJSONError(w, http.StatusUnauthorized, "account not found")
				return
			}
			if user.Status != "active" {
				middleware.WriteJSONError(w, http.StatusForbidden, "account is not active")
				return
			}
			var req createAccessCodeRequest
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&req)
			}
			ttl := 10 * time.Minute
			target := actor.UserID
			targetPtr := &target
			scope := "self"
			if user.Type == "admin" {
				// Admin codes are not bound to an account: redemption decides
				// whether to create a new ordinary user or pair an existing
				// account.
				targetPtr = nil
				ttl = 24 * time.Hour
				scope = "new-or-existing"
				if req.TTLSeconds > 0 {
					ttl = time.Duration(req.TTLSeconds) * time.Second
				}
			}
			code, expiresAt, err := authSvc.IssueAccessCode(r.Context(), targetPtr, &actor.UserID, ttl)
			if err != nil {
				middleware.WriteJSONError(w, http.StatusInternalServerError, "could not create access code")
				return
			}
			middleware.WriteJSON(w, http.StatusCreated, map[string]any{
				"code":      code,
				"expiresAt": expiresAt.UTC().Format(time.RFC3339),
				"scope":     scope,
			})
		}))))

	mux.HandleFunc("POST /api/v1/access-codes/redeem", func(w http.ResponseWriter, r *http.Request) {
		var req redeemAccessCodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid access code request")
			return
		}
		if req.DeviceLabel == "" {
			req.DeviceLabel = "mako-client-android"
		}
		user, token, created, err := authSvc.RedeemAccessCode(
			r.Context(), req.Code, req.Username, req.Password, req.DeviceLabel,
		)
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrInvalidLogin):
				middleware.WriteJSONError(w, http.StatusUnauthorized, "invalid username or password")
			case errors.Is(err, auth.ErrAccessCodeExpired):
				middleware.WriteJSONError(w, http.StatusUnauthorized, "access code has expired")
			case errors.Is(err, auth.ErrAccessCodeUsed):
				middleware.WriteJSONError(w, http.StatusUnauthorized, "access code has already been used")
			case errors.Is(err, auth.ErrAccessCodeInvalid):
				middleware.WriteJSONError(w, http.StatusUnauthorized, "invalid access code")
			default:
				middleware.WriteJSONError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		response := map[string]any{
			"created": created,
			"user":    user,
			"token":   token,
		}
		if pairingSvc != nil {
			response["certSha256"] = pairingSvc.CertSHA256()
		}
		if serverInfo != nil {
			response["serverId"] = serverInfo.ServerID
			response["serverName"] = serverInfo.Name
		}
		middleware.WriteJSON(w, http.StatusOK, response)
	})
}
