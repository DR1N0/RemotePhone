// This file mounts admin's user-management routes: list, promote/demote,
// ban/unban, and credential listing/revocation. Every handler delegates
// straight to admin.Service, which itself enforces the active-admin check
// (auth.RequireActiveAdmin) 鈥?there is no separate requireAdmin HTTP
// middleware, matching the precedent instance.Service's own admin-managed
// instance routes already set.
package adminapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"mako/internal/controlplane/admin"
	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/transport"

	"github.com/google/uuid"
)

func adminErrorStatus(err error) int {
	switch {
	case errors.Is(err, auth.ErrNotAdmin):
		return http.StatusForbidden
	case errors.Is(err, auth.ErrUserNotFound),
		errors.Is(err, admin.ErrGuestAttachmentNotFound),
		errors.Is(err, admin.ErrAccountCredentialNotFound):
		return http.StatusNotFound
	case errors.Is(err, admin.ErrLastActiveAdmin),
		errors.Is(err, audit.ErrInvalidCursor):
		return http.StatusConflict
	case errors.Is(err, audit.ErrInvalidLimit):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeAdminError(w http.ResponseWriter, err error) {
	status := adminErrorStatus(err)
	if status == http.StatusInternalServerError {
		panic(err)
	}
	middleware.WriteJSONError(w, status, err.Error())
}

// parsePaginationLimit reads the "limit" query param, defaulting to 20,
// and reports whether it falls within [1, 100] 鈥?mirrors TS's
// parsePaginationLimit used by both the users and audit list routes.
func parsePaginationLimit(r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 100 {
		return 0, false
	}
	return limit, true
}

func cursorParam(r *http.Request) *string {
	cursor := r.URL.Query().Get("cursor")
	if cursor == "" {
		return nil
	}
	return &cursor
}

type setUserTypeRequest struct {
	Type string `json:"type"`
}

// RegisterAdminManagementRoutes mounts user/credential/ban routes, mirroring
// routes/admin.ts's user-management half.
func RegisterAdminManagementRoutes(mux *http.ServeMux, adminSvc *admin.Service, authSvc *auth.Service, transportSvc *transport.Service) {
	requireAuth := middleware.RequireOwner(authSvc)
	requireAccount := func(next http.Handler) http.Handler { return next }

	mux.Handle("GET /api/v1/admin/users", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, ok := parsePaginationLimit(r)
		if !ok {
			middleware.WriteJSONError(w, http.StatusBadRequest, "limit must be an integer from 1 to 100")
			return
		}
		caller := middleware.GetAuthenticatedUser(r.Context())
		items, nextCursor, err := adminSvc.ListUsers(r.Context(), caller.UserID, limit, cursorParam(r))
		if err != nil {
			writeAdminError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nextCursor})
	}))))

	mux.Handle("GET /api/v1/admin/users/{id}/credentials", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		items, err := adminSvc.ListCredentials(r.Context(), caller.UserID, r.PathValue("id"))
		if err != nil {
			writeAdminError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}))))

	mux.Handle("POST /api/v1/admin/users/{id}/credentials/{credentialId}/revoke",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			err := adminSvc.RevokeAccountCredential(r.Context(), r.PathValue("id"), r.PathValue("credentialId"), caller.UserID, actor)
			if err != nil {
				writeAdminError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))))

	mux.Handle("POST /api/v1/admin/users/{id}/type", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req setUserTypeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || (req.Type != "registered" && req.Type != "admin") {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid user type request")
			return
		}
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		if err := adminSvc.SetUserType(r.Context(), r.PathValue("id"), req.Type, caller.UserID, actor); err != nil {
			writeAdminError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))))

	registerBanRoutes(mux, requireAuth, requireAccount, adminSvc, transportSvc)
}
