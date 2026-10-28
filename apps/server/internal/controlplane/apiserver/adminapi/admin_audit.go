package adminapi

import (
	"net/http"

	"remotephone/internal/controlplane/admin"
	"remotephone/internal/controlplane/apiserver/middleware"
	"remotephone/internal/controlplane/auth"
)

// RegisterAdminAuditRoutes mounts the audit-log retrieval route, mirroring
// routes/admin.ts's GET /api/v1/admin/audit.
func RegisterAdminAuditRoutes(mux *http.ServeMux, adminSvc *admin.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireOwner(authSvc)
	requireAccount := func(next http.Handler) http.Handler { return next }

	mux.Handle("GET /api/v1/admin/audit", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit, ok := parsePaginationLimit(r)
		if !ok {
			middleware.WriteJSONError(w, http.StatusBadRequest, "limit must be an integer from 1 to 100")
			return
		}
		caller := middleware.GetAuthenticatedUser(r.Context())
		items, nextCursor, err := adminSvc.ListAudit(r.Context(), caller.UserID, limit, cursorParam(r))
		if err != nil {
			writeAdminError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"items": items, "nextCursor": nextCursor})
	}))))
}
