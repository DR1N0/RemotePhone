package clientapi

import (
	"encoding/json"
	"errors"
	"net/http"

	"mako/internal/controlplane/apiserver/middleware"
	"mako/internal/controlplane/pairing"
)

type pairingVerifyRequest struct {
	PIN string `json:"pin"`
}

// RegisterPairingRoutes mounts the unauthenticated trust-bootstrap routes.
// These are intentionally the only client routes reachable without a bearer
// token besides auth: a new client has no token until it has paired.
func RegisterPairingRoutes(mux *http.ServeMux, svc *pairing.Service) {
	if svc == nil {
		return
	}

	mux.HandleFunc("GET /api/v1/pairing/info", func(w http.ResponseWriter, r *http.Request) {
		middleware.WriteJSON(w, http.StatusOK, map[string]any{
			"required":   true,
			"certSha256": svc.CertSHA256(),
		})
	})

	mux.HandleFunc("POST /api/v1/pairing/verify", func(w http.ResponseWriter, r *http.Request) {
		var req pairingVerifyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			middleware.WriteJSONError(w, http.StatusBadRequest, "invalid pairing request")
			return
		}
		if err := svc.Verify(req.PIN); err != nil {
			switch {
			case errors.Is(err, pairing.ErrTooManyAttempts):
				middleware.WriteJSONError(w, http.StatusTooManyRequests, "too many pairing attempts; try again later")
			default:
				middleware.WriteJSONError(w, http.StatusUnauthorized, "invalid pairing PIN")
			}
			return
		}
		middleware.WriteJSON(w, http.StatusOK, map[string]any{
			"certSha256": svc.CertSHA256(),
		})
	})
}
