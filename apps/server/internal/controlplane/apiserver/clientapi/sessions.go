package clientapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"remotephone/internal/controlplane/apiserver/middleware"
	"remotephone/internal/controlplane/audit"
	"remotephone/internal/controlplane/auth"
	"remotephone/internal/controlplane/instance"
	"remotephone/internal/controlplane/session"
	"remotephone/internal/controlplane/transport"

	"github.com/google/uuid"
)

func sessionErrorStatus(err error) int {
	var transportUnavailable *transport.ErrTransportUnavailable
	switch {
	case errors.As(err, &transportUnavailable):
		return http.StatusBadGateway
	case errors.Is(err, session.ErrInvalidRequest), errors.Is(err, session.ErrSessionInviteInvalid):
		return http.StatusBadRequest
	case errors.Is(err, session.ErrSessionConflict):
		return http.StatusConflict
	case errors.Is(err, session.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, session.ErrSessionNotFound), errors.Is(err, session.ErrAttachmentNotFound):
		return http.StatusNotFound
	case errors.Is(err, instance.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, instance.ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, instance.ErrForbidden):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func writeSessionError(w http.ResponseWriter, err error) {
	status := sessionErrorStatus(err)
	if status == http.StatusInternalServerError {
		panic(err)
	}
	middleware.WriteJSONError(w, status, err.Error())
}

// RegisterSessionRoutes mounts session/attachment lifecycle and role-
// transition routes, mirroring routes/sessions.ts. The retired join/resume-
// without-attachment-credential routes become 410 Gone tombstones, same
// pattern as the auth phase's pairing-invite tombstones. Session moderation
// (promote/demote/invites) is owner-scoped only — there is no cross-owner
// session route on adminapi, matching TS exactly.
func RegisterSessionRoutes(mux *http.ServeMux, sessionSvc *session.Service, transportSvc *transport.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")
	requireAttachment := middleware.RequireScope("attachment")

	mux.Handle("POST /api/v1/sessions", requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		var body struct {
			InstanceID            string `json:"instanceId"`
			OwnerClientID         string `json:"ownerClientId"`
			OwnerAbsenceTimeoutMs *int64 `json:"ownerAbsenceTimeoutMs"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.InstanceID == "" || body.OwnerClientID == "" {
			middleware.WriteJSONError(w, http.StatusBadRequest, "instanceId and ownerClientId are required")
			return
		}
		record, attachmentID, token, err := sessionSvc.CreateSession(r.Context(), body.InstanceID, body.OwnerClientID, body.OwnerAbsenceTimeoutMs, *caller)
		if err != nil {
			writeSessionError(w, err)
			return
		}
		// attachmentId is explicit, not left for the caller to infer from
		// session.attachments: CreateSession can now return either the
		// owner attachment (fresh or reclaimed) or a new participant
		// attachment when the owner is already connected elsewhere — see
		// session.Service.CreateSession's doc comment.
		middleware.WriteJSON(w, http.StatusCreated, map[string]any{"session": record, "attachmentId": attachmentID, "attachmentToken": token})
	}))))

	mux.HandleFunc("POST /api/v1/sessions/{id}/join", func(w http.ResponseWriter, r *http.Request) {
		middleware.WriteJSONError(w, http.StatusGone, "session join is retired; redeem a session invite")
	})
	mux.HandleFunc("POST /api/v1/sessions/{id}/resume", func(w http.ResponseWriter, r *http.Request) {
		middleware.WriteJSONError(w, http.StatusGone, "session resume requires an attachment credential")
	})

	// Session reads conceal private existence from valid but unrelated
	// accounts: a forbidden caller gets the same 404 as a nonexistent
	// session, not 403.
	mux.Handle("GET /api/v1/sessions/{id}", requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		caller := middleware.GetAuthenticatedUser(r.Context())
		record, err := sessionSvc.GetSession(r.Context(), r.PathValue("id"), *caller)
		if err != nil {
			if errors.Is(err, session.ErrForbidden) {
				middleware.WriteJSONError(w, http.StatusNotFound, fmt.Sprintf("session not found: %s", r.PathValue("id")))
				return
			}
			writeSessionError(w, err)
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{"session": record})
	})))

	mux.Handle("POST /api/v1/sessions/{id}/attachments/{attachmentId}/resume",
		requireAuth(requireAttachment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			attachment, token, err := sessionSvc.ResumeAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), *caller)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"attachment": attachment, "attachmentToken": token})
		}))))

	mux.Handle("POST /api/v1/sessions/{id}/attachments/{attachmentId}/credentials/refresh",
		requireAuth(requireAttachment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			token, err := sessionSvc.RefreshAttachmentCredential(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), *caller)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"attachmentToken": token})
		}))))

	// No scope middleware: session.Service.DisconnectAttachment branches on
	// caller.Scope itself (the attachment disconnecting itself, or the
	// account owner kicking it).
	mux.Handle("POST /api/v1/sessions/{id}/attachments/{attachmentId}/disconnect",
		requireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			record, err := sessionSvc.DisconnectAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), *caller, actor)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if _, err := transportSvc.TeardownPendingAttachment(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), actor); err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"session": record})
		})))

	mux.Handle("POST /api/v1/sessions/{id}/attachments/{attachmentId}/promote",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			attachment, err := transportSvc.TransitionAttachmentRole(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), "participant", *caller, actor)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"attachment": attachment})
		}))))

	mux.Handle("POST /api/v1/sessions/{id}/attachments/{attachmentId}/demote",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			attachment, err := transportSvc.TransitionAttachmentRole(r.Context(), r.PathValue("id"), r.PathValue("attachmentId"), "observer", *caller, actor)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"attachment": attachment})
		}))))
}
