package clientapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/audit"
	"mako/internal/controlplane/auth"
	"mako/internal/controlplane/session"

	"github.com/google/uuid"
)

// bearerToken extracts the raw token from an Authorization: Bearer header,
// or "" if absent/malformed. Duplicated from middleware.RequireAuth's
// identical extraction (unexported there) because session-invite redemption
// is the one route that must handle BOTH an authenticated request
// (mode=registered) and a fully anonymous one (mode=guest) — it can't use
// RequireAuth, which rejects a missing token outright.
func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	return strings.TrimPrefix(header, prefix)
}

// RegisterSessionInviteRoutes mounts session-invite create/redeem, mirroring
// routes/invites.ts — except redemption takes a 6-digit PIN
// ("pin", not "token") per the PIN-based invite redesign (see
// docs/architecture/decisions-log.md, 2026-10-15), and guest display-name
// validation is NOT re-checked here: session.Service.RedeemSessionInvite
// is the single validation point (finding from the original TS research:
// routes/invites.ts duplicated session-invite-service.ts's identical check).
func RegisterSessionInviteRoutes(mux *http.ServeMux, sessionSvc *session.Service, authSvc *auth.Service) {
	requireAuth := middleware.RequireAuth(authSvc)
	requireAccount := middleware.RequireScope("account")

	mux.Handle("POST /api/v1/sessions/{id}/invites",
		requireAuth(requireAccount(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			caller := middleware.GetAuthenticatedUser(r.Context())
			actor := audit.NewContext(&caller.UserID, &caller.CredentialID, uuid.NewString())
			pin, expiresAt, err := sessionSvc.CreateSessionInvite(r.Context(), r.PathValue("id"), *caller, actor)
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusCreated, map[string]any{"pin": pin, "expiresAt": expiresAt})
		}))))

	mux.HandleFunc("POST /api/v1/session-invites/redeem", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Pin         string  `json:"pin"`
			Mode        string  `json:"mode"`
			DisplayName *string `json:"displayName"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "a JSON body is required")
			return
		}
		if body.Pin == "" {
			middleware.WriteJSONError(w, http.StatusBadRequest, "pin is required")
			return
		}

		switch body.Mode {
		case session.SessionInviteModeGuest:
			if r.Header.Get("Authorization") != "" {
				middleware.WriteJSONError(w, http.StatusBadRequest, "guest redemption must not include an Authorization header")
				return
			}
			attachment, token, user, err := sessionSvc.RedeemSessionInvite(r.Context(), body.Pin, session.SessionInviteModeGuest, nil, body.DisplayName, nil, uuid.NewString())
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusCreated, map[string]any{"attachment": attachment, "attachmentToken": token, "user": user})

		case session.SessionInviteModeRegistered:
			token := bearerToken(r)
			if token == "" {
				middleware.WriteJSONError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			authed, err := authSvc.Authenticate(r.Context(), token)
			if err != nil {
				panic(fmt.Errorf("authenticate: %w", err))
			}
			if authed == nil {
				middleware.WriteJSONError(w, http.StatusUnauthorized, "invalid or revoked credential")
				return
			}
			if authed.Scope != "account" {
				middleware.WriteJSONError(w, http.StatusForbidden, "credential scope is not authorized")
				return
			}
			attachment, newToken, user, err := sessionSvc.RedeemSessionInvite(r.Context(), body.Pin, session.SessionInviteModeRegistered, &authed.UserID, nil, &authed.CredentialID, uuid.NewString())
			if err != nil {
				writeSessionError(w, err)
				return
			}
			middleware.WriteJSON(w, http.StatusCreated, map[string]any{"attachment": attachment, "attachmentToken": newToken, "user": user})

		default:
			middleware.WriteJSONError(w, http.StatusBadRequest, "mode must be registered or guest")
		}
	})
}
