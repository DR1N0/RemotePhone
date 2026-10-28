package clientapi

import (
	"encoding/json"
	"errors"
	"net/http"

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

// RegisterInstanceRoutes mounts the owner-scoped instance CRUD/lifecycle
// routes on mux, mirroring routes/instances.ts's owner-scoping: every route
// here acts only on instances owned by the authenticated caller. Cross-owner
// instance management lives on adminapi, not here.
func RegisterInstanceRoutes(mux *http.ServeMux, svc *instance.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

	mux.Handle("POST /api/v1/instances", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		var req instance.CreateInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid instance request")
			return
		}
		created, err := svc.CreateManagedInstance(r.Context(), req, caller.UserID)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusCreated, map[string]any{"instance": created})
	}))))

	mux.Handle("GET /api/v1/instances", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		instances, err := svc.ListManagedInstances(r.Context(), caller.UserID)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"instances": instances})
	}))))

	mux.Handle("GET /api/v1/instances/{id}", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		got, err := svc.GetManagedInstance(r.Context(), r.PathValue("id"), caller.UserID)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"instance": got})
	}))))

	mux.Handle("POST /api/v1/instances/{id}/start", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		started, err := svc.StartManagedInstance(r.Context(), r.PathValue("id"), caller.UserID, actor)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"instance": started})
	}))))

	mux.Handle("POST /api/v1/instances/{id}/stop", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		stopped, err := svc.StopManagedInstance(r.Context(), r.PathValue("id"), caller.UserID, actor)
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"instance": stopped})
	}))))

	mux.Handle("DELETE /api/v1/instances/{id}", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		if err := svc.DeleteManagedInstance(r.Context(), r.PathValue("id"), caller.UserID, actor); err != nil {
			writeInstanceError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))))
}
