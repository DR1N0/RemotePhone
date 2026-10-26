package adminapi

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"
)

type ownerAccessCodeRequest struct {
	Username   string `json:"username"`
	TTLSeconds int    `json:"ttlSeconds"`
}

type ownerResetPasswordRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// RegisterOwnerRoutes mounts the loopback-only owner console. Reaching this
// listener already requires host/SSH access, which is the owner proof; routes
// intentionally do not require a bearer token. Host/Origin checks and a JSON
// content-type requirement keep a browser on the same machine from driving it
// via CSRF or DNS rebinding.
func RegisterOwnerRoutes(mux *http.ServeMux, authSvc *auth.Service) {
	if authSvc == nil {
		return
	}
	guard := func(next http.HandlerFunc) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isLoopbackRequest(r) {
				middleware.WriteJSONError(w, http.StatusForbidden, "owner API is loopback-only")
				return
			}
			if r.Header.Get("Origin") != "" {
				middleware.WriteJSONError(w, http.StatusForbidden, "browser origins are not allowed")
				return
			}
			if r.Method == http.MethodPost &&
				!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				middleware.WriteJSONError(w, http.StatusUnsupportedMediaType, "application/json required")
				return
			}
			next(w, r)
		})
	}

	mux.Handle("POST /api/v1/owner/access-codes", guard(func(w http.ResponseWriter, r *http.Request) {
		var req ownerAccessCodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid owner request")
			return
		}
		var target *string
		if strings.TrimSpace(req.Username) != "" {
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
		code, expiresAt, err := authSvc.IssueAccessCode(r.Context(), target, nil, ttl)
		if err != nil {
			middleware.WriteJSONError(w, http.StatusInternalServerError, "could not create access code")
			return
		}
		middleware.WriteJSON(w, http.StatusCreated, map[string]any{
			"code":      code,
			"expiresAt": expiresAt.UTC().Format(time.RFC3339),
			"target":    req.Username,
		})
	}))

	mux.Handle("POST /api/v1/owner/users/reset-password", guard(func(w http.ResponseWriter, r *http.Request) {
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
	}))
}

func isLoopbackRequest(r *http.Request) bool {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
