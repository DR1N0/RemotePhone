package clientapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/instance"
	"mako/internal/controlplane/session"
	"mako/internal/controlplane/transport"

	"github.com/google/uuid"
)

// signalingTarget is the result of authorizeSignalingTarget: the caller's
// own session+attachment, already confirmed connected and transportable.
type signalingTarget struct {
	sessionRecord session.SessionRecord
	attachment    session.AttachmentRecord
}

// authorizeSignalingTarget mirrors routes/signaling.ts's helper of the same
// name: the caller's attachment credential must be bound to EXACTLY the
// sessionID/attachmentID the request names — a valid attachment credential
// for a DIFFERENT attachment must not be usable to signal this one.
func authorizeSignalingTarget(ctx context.Context, caller *auth.AuthenticatedUser, sessionSvc *session.Service, sessionID, attachmentID string) (*signalingTarget, error) {
	if caller.Scope != "attachment" || caller.SessionID == nil || *caller.SessionID != sessionID || caller.AttachmentID == nil || *caller.AttachmentID != attachmentID {
		return nil, nil
	}
	record, err := sessionSvc.GetSessionUnchecked(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if record.Status != "active" {
		return nil, fmt.Errorf("%w: session is not transportable: %s", session.ErrInvalidRequest, sessionID)
	}
	var attachment *session.AttachmentRecord
	for i := range record.Attachments {
		if record.Attachments[i].ID == attachmentID {
			attachment = &record.Attachments[i]
			break
		}
	}
	if attachment == nil {
		return nil, fmt.Errorf("%w: %s", session.ErrAttachmentNotFound, attachmentID)
	}
	if attachment.Status != "connected" {
		return nil, fmt.Errorf("%w: attachment is not connected: %s", session.ErrInvalidRequest, attachmentID)
	}
	return &signalingTarget{sessionRecord: record, attachment: *attachment}, nil
}

// RegisterSignalingRoutes mounts the WebRTC SDP offer/ICE-candidate relay
// routes, mirroring routes/signaling.ts.
func RegisterSignalingRoutes(mux *http.ServeMux, sessionSvc *session.Service, instanceSvc *instance.Service, transportSvc *transport.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAttachment := middleware.RequireScope("attachment")

	mux.Handle("POST /api/v1/signaling/session",
		requireAuth(requireAttachment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				SessionID    string `json:"sessionId"`
				AttachmentID string `json:"attachmentId"`
				SdpOffer     string `json:"sdpOffer"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SessionID == "" || body.AttachmentID == "" || body.SdpOffer == "" {
				middleware.WriteJSONError(w, http.StatusBadRequest, "sessionId, attachmentId, and sdpOffer are required")
				return
			}
			caller := middleware.GetAuthenticatedUser(r.Context())
			target, err := authorizeSignalingTarget(r.Context(), caller, sessionSvc, body.SessionID, body.AttachmentID)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if target == nil {
				middleware.WriteJSONError(w, http.StatusForbidden, "attachment credential does not match signaling target")
				return
			}
			instanceRecord, err := instanceSvc.GetManagedInstanceUnchecked(r.Context(), target.sessionRecord.InstanceID)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if instanceRecord.RuntimeHandle == nil {
				middleware.WriteJSONError(w, http.StatusBadRequest, fmt.Sprintf("instance is not running: %s", instanceRecord.ID))
				return
			}

			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			if _, err := transportSvc.EnsureAttachmentTransport(r.Context(), target.sessionRecord.ID, target.attachment.ID, actor); err != nil {
				writeSessionError(w, err)
				return
			}
			sdpAnswer, err := transportSvc.RelayOffer(r.Context(), instanceRecord.ID, target.attachment.ID, body.SdpOffer)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"sdpAnswer": sdpAnswer})
		}))))

	mux.Handle("POST /api/v1/signaling/ice-candidate",
		requireAuth(requireAttachment(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				SessionID    string `json:"sessionId"`
				AttachmentID string `json:"attachmentId"`
				Candidate    string `json:"candidate"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SessionID == "" || body.AttachmentID == "" {
				middleware.WriteJSONError(w, http.StatusBadRequest, "sessionId and attachmentId are required")
				return
			}
			caller := middleware.GetAuthenticatedUser(r.Context())
			target, err := authorizeSignalingTarget(r.Context(), caller, sessionSvc, body.SessionID, body.AttachmentID)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if target == nil {
				middleware.WriteJSONError(w, http.StatusForbidden, "attachment credential does not match signaling target")
				return
			}
			instanceRecord, err := instanceSvc.GetManagedInstanceUnchecked(r.Context(), target.sessionRecord.InstanceID)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			if instanceRecord.RuntimeHandle == nil {
				middleware.WriteJSONError(w, http.StatusBadRequest, fmt.Sprintf("instance is not running: %s", instanceRecord.ID))
				return
			}
			if err := transportSvc.RelayICECandidate(r.Context(), instanceRecord.ID, target.attachment.ID, body.Candidate); err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusOK, map[string]any{"status": "ok"})
		}))))
}
