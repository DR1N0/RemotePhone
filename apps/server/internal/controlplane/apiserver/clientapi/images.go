package clientapi

import (
	"errors"
	"net/http"

	"remotephone/internal/controlplane/apiserver/middleware"
	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/image"

	"github.com/google/uuid"
)

func imageErrorStatus(err error) int {
	switch {
	case errors.Is(err, image.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, image.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, image.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

func writeImageError(w http.ResponseWriter, err error) {
	status := imageErrorStatus(err)
	if status == http.StatusInternalServerError {
		panic(err)
	}
	middleware.WriteJSONError(w, status, err.Error())
}

// RegisterImageRoutes mounts the owner-scoped base-image capture route on
// mux — capturing never affects anything live (see image.Service's doc
// comment), so unlike promote (adminapi), it doesn't need admin
// authorization to stay safe.
func RegisterImageRoutes(mux *http.ServeMux, svc *image.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

	mux.Handle("POST /api/v1/instances/{id}/capture-candidate-image", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		captured, err := svc.CaptureCandidateImage(r.Context(), r.PathValue("id"), caller.UserID, actor)
		if err != nil {
			writeImageError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusCreated, map[string]any{"baseImage": captured})
	}))))
}
