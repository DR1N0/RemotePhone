package adminapi

import (
	"errors"
	"net/http"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/image"

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

// RegisterImageRoutes mounts promote (the only operation in this package
// that changes what any instance resolves to 鈥?hence admin-only, behind
// adminapi's loopback/login barrier) and candidate listing, so an admin has
// a way to discover candidate ids before promoting one.
func RegisterImageRoutes(mux *http.ServeMux, svc *image.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireOwner(authSvc)
	requireAccount := func(next http.Handler) http.Handler { return next }

	mux.Handle("GET /api/v1/admin/images", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		templateID := r.URL.Query().Get("templateId")
		if templateID == "" {
			middleware.WriteJSONError(w, http.StatusBadRequest, "templateId query parameter is required")
			return
		}
		items, err := svc.ListCandidateImages(r.Context(), templateID)
		if err != nil {
			writeImageError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items})
	}))))

	mux.Handle("POST /api/v1/admin/images/{candidateId}/promote", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
		template, err := svc.PromoteBaseImage(r.Context(), r.PathValue("candidateId"), caller.UserID, actor)
		if err != nil {
			writeImageError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"template": template})
	}))))
}
