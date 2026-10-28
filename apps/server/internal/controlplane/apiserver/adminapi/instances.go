package adminapi

import (
	"errors"
	"net/http"

	"remotephone/internal/controlplane/admin"
	"remotephone/internal/controlplane/apiserver/middleware"
	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/instance"

	"github.com/google/uuid"
)

func instanceErrorStatus(err error) int {
	switch {
	case errors.Is(err, instance.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, instance.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, instance.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeInstanceError(w http.ResponseWriter, err error) {
	status := instanceErrorStatus(err)
	if status == http.StatusInternalServerError {
		panic(err)
	}
	middleware.WriteJSONError(w, status, err.Error())
}

// RegisterInstanceRoutes mounts cross-owner instance list/start/stop/delete
// on mux. Listing (routes/admin.ts's GET /api/v1/admin/instances) goes
// through adminSvc.ListInstances, not svc directly 鈥?cross-owner listing is
// the admin package's Phase 3 port (instance.Service only exposes the
// underlying ListAllInstances it calls).
func RegisterInstanceRoutes(mux *http.ServeMux, svc *instance.Service, adminSvc *admin.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireOwner(authSvc)
	requireAccount := func(next http.Handler) http.Handler { return next }

	mux.Handle("GET /api/v1/admin/instances", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		items, err := adminSvc.ListInstances(r.Context(), caller.UserID)
		if err != nil {
			writeAdminError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}))))

	mux.Handle("POST /api/v1/admin/instances/{id}/start", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		started, err := svc.StartAdminManagedInstance(r.Context(), r.PathValue("id"), caller.UserID, actor)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"instance": started})
	}))))

	mux.Handle("POST /api/v1/admin/instances/{id}/stop", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		stopped, err := svc.StopAdminManagedInstance(r.Context(), r.PathValue("id"), caller.UserID, actor)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"instance": stopped})
	}))))

	mux.Handle("DELETE /api/v1/admin/instances/{id}", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		if err := svc.DeleteAdminManagedInstance(r.Context(), r.PathValue("id"), caller.UserID, actor); err != nil {
			writeInstanceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))))
}
