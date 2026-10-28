package adminapi

import (
	"net/http"

	"remotephone/internal/controlplane/admin"
	"remotephone/internal/controlplane/apiserver/middleware"
	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/transport"

	"github.com/google/uuid"
)

// teardownRevokedAttachments drives worker-side transport teardown for
// every attachment a DB-only admin mutation (ban, guest-revoke) returned as
// still needing it 鈥?the second half of the two-step pattern
// clientapi/sessions.go's disconnect handler already uses. A teardown
// failure here is reported as the request's error, same as any other
// transport call; the DB mutation has already committed either way.
func teardownRevokedAttachments(r *http.Request, transportSvc *transport.Service, actor audit.Context, targets []admin.TeardownTarget) error {
	for _, target := range targets {
		if _, err := transportSvc.TeardownPendingAttachment(r.Context(), target.SessionID, target.AttachmentID, actor); err != nil {
			return err
		}
	}
	return nil
}

func registerBanRoutes(
	mux *http.ServeMux,
	requireAuth, requireAccount func(http.Handler) http.Handler,
	adminSvc *admin.Service,
	transportSvc *transport.Service,
) {
	setBanStatus := func(status string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			targets, err := adminSvc.SetBanStatus(r.Context(), r.PathValue("id"), status, caller.UserID, actor)
			if err != nil {
				writeAdminError(w, err)
				return
			}
			if err := teardownRevokedAttachments(r, transportSvc, actor, targets); err != nil {
				writeAdminError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}
	mux.Handle("POST /api/v1/admin/users/{id}/ban", requireAuth(requireAccount(setBanStatus("banned"))))
	mux.Handle("POST /api/v1/admin/users/{id}/unban", requireAuth(requireAccount(setBanStatus("active"))))

	mux.Handle("POST /api/v1/admin/sessions/{id}/attachments/{attachmentId}/revoke",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			target, err := adminSvc.RevokeGuestAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), caller.UserID, actor)
			if err != nil {
				writeAdminError(w, err)
				return
			}
			if err := teardownRevokedAttachments(r, transportSvc, actor, []admin.TeardownTarget{target}); err != nil {
				writeAdminError(w, err)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}))))
}
