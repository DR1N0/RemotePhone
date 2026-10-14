package adminapi

import (
	"errors"
	"net/http"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"

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

// RegisterInstanceRoutes mounts cross-owner instance start/stop/delete on
// mux. Listing every instance across all owners (routes/admin.ts's
// GET /api/v1/admin/instances, via admin-service.ts's listInstances) isn't
// built here — it belongs to the admin package's own Phase 3 port, not this
// phase's instance package.
func RegisterInstanceRoutes(mux *http.ServeMux, svc *instance.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

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
