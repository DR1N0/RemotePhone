package clientapi

import (
	"net/http"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
)

// RegisterTemplateRoutes exposes the host's discoverable runtime templates to
// an authenticated account, so a client can offer a real template picker
// instead of asking the user to type a hardcoded id. Discovery happens by
// spawning a short-lived worker (see instance.Service.ListTemplates); it is
// account-scoped, not admin-only, because every instance owner needs it.
func RegisterTemplateRoutes(mux *http.ServeMux, svc *instance.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

	mux.Handle("GET /api/v1/templates", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		templates, err := svc.ListTemplates(r.Context())
		if err != nil {
			writeInstanceError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"templates": templates})
	}))))
}
