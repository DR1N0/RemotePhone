package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	runtimepkg "mako-worker/internal/runtime"
	"mako-worker/internal/transport"
)

type Dependencies struct {
	WorkerID        string
	ListenBaseURL   string
	HostInventory   HostInventory
	RuntimeRegistry *runtimepkg.Registry
	TransportStore  *transport.Store
}

func NewRouter(deps Dependencies) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, HealthResponse{
			Status:            "ok",
			WorkerID:          deps.WorkerID,
			HostOS:            deps.HostInventory.HostOS,
			Arch:              deps.HostInventory.Arch,
			InstalledRuntimes: listInstalledRuntimes(deps.RuntimeRegistry),
		})
	})

	mux.HandleFunc("GET /v1/templates", func(w http.ResponseWriter, r *http.Request) {
		if deps.RuntimeRegistry == nil {
			writeJSON(w, http.StatusOK, TemplateListResponse{Templates: []runtimepkg.Template{}})
			return
		}

		templates, err := deps.RuntimeRegistry.DiscoverTemplates()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}

		writeJSON(w, http.StatusOK, TemplateListResponse{Templates: templates})
	})

	mux.HandleFunc("POST /v1/instances/start", func(w http.ResponseWriter, r *http.Request) {
		if deps.RuntimeRegistry == nil {
			writeErrorMessage(w, http.StatusBadRequest, "runtime registry is not configured")
			return
		}

		var req StartInstanceRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.RuntimeClass) == "" || strings.TrimSpace(req.TemplateID) == "" {
			writeErrorMessage(w, http.StatusBadRequest, "instanceId, runtimeClass, and templateId are required")
			return
		}

		handle, err := deps.RuntimeRegistry.Start(runtimepkg.StartRequest{
			InstanceID:   req.InstanceID,
			RuntimeClass: req.RuntimeClass,
			TemplateID:   req.TemplateID,
			LaunchConfig: req.LaunchConfig,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, runtimepkg.ErrAdapterNotFound) || errors.Is(err, runtimepkg.ErrTemplateNotFound) {
				status = http.StatusBadRequest
			}
			writeError(w, status, err)
			return
		}

		writeJSON(w, http.StatusOK, StartInstanceResponse{
			State:  "starting",
			Handle: handle,
		})
	})

	mux.HandleFunc("GET /v1/instances/{id}", func(w http.ResponseWriter, r *http.Request) {
		if deps.RuntimeRegistry == nil {
			writeErrorMessage(w, http.StatusBadRequest, "runtime registry is not configured")
			return
		}

		instanceID := strings.TrimSpace(r.PathValue("id"))
		if instanceID == "" {
			writeErrorMessage(w, http.StatusBadRequest, "instance id is required")
			return
		}

		status, err := deps.RuntimeRegistry.Inspect(instanceID)
		if err != nil {
			httpStatus := http.StatusInternalServerError
			if errors.Is(err, runtimepkg.ErrInstanceNotFound) {
				httpStatus = http.StatusNotFound
			}
			writeError(w, httpStatus, err)
			return
		}

		writeJSON(w, http.StatusOK, InspectInstanceResponse{
			State:     status.State,
			Handle:    status.Handle,
			LastError: status.LastError,
		})
	})

	mux.HandleFunc("POST /v1/instances/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		if deps.RuntimeRegistry == nil {
			writeErrorMessage(w, http.StatusBadRequest, "runtime registry is not configured")
			return
		}

		instanceID := strings.TrimSpace(r.PathValue("id"))
		if instanceID == "" {
			writeErrorMessage(w, http.StatusBadRequest, "instance id is required")
			return
		}

		if deps.TransportStore != nil {
			deps.TransportStore.StopAndDeleteByPrefix(instanceID + ":")
		}

		status, err := deps.RuntimeRegistry.Stop(instanceID)
		if err != nil {
			httpStatus := http.StatusInternalServerError
			if errors.Is(err, runtimepkg.ErrInstanceNotFound) {
				httpStatus = http.StatusNotFound
			}
			writeError(w, httpStatus, err)
			return
		}

		writeJSON(w, http.StatusOK, StopInstanceResponse{
			State:     status.State,
			Handle:    status.Handle,
			LastError: status.LastError,
		})
	})

	mux.HandleFunc("POST /v1/transport/session", func(w http.ResponseWriter, r *http.Request) {
		if deps.RuntimeRegistry == nil {
			writeErrorMessage(w, http.StatusBadRequest, "runtime registry is not configured")
			return
		}

		var req CreateTransportSessionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.AttachmentID) == "" || strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.RuntimeClass) == "" || strings.TrimSpace(req.Role) == "" {
			writeErrorMessage(w, http.StatusBadRequest, "sessionId, attachmentId, instanceId, runtimeClass, and role are required")
			return
		}
		if !validTransportRole(req.Role) {
			writeErrorMessage(w, http.StatusBadRequest, "role must be owner, participant, or observer")
			return
		}

		transportSession, err := deps.RuntimeRegistry.CreateTransportSession(runtimepkg.TransportSessionRequest{
			SessionID:     req.SessionID,
			AttachmentID:  req.AttachmentID,
			InstanceID:    req.InstanceID,
			RuntimeClass:  req.RuntimeClass,
			Role:          req.Role,
			RuntimeHandle: req.RuntimeHandle,
			WorkerBaseURL: strings.TrimRight(deps.ListenBaseURL, "/"),
		})
		if err != nil {
			status := http.StatusBadRequest
			if !errors.Is(err, runtimepkg.ErrAdapterNotFound) {
				status = http.StatusInternalServerError
			}
			writeError(w, status, err)
			return
		}

		writeJSON(w, http.StatusOK, TransportSessionResponse{
			SessionID:     transportSession.SessionID,
			AttachmentID:  transportSession.AttachmentID,
			InstanceID:    transportSession.InstanceID,
			RuntimeClass:  transportSession.RuntimeClass,
			Mode:          transportSession.Mode,
			Status:        transportSession.Status,
			WorkerBaseURL: transportSession.WorkerBaseURL,
			SessionToken:  transportSession.SessionToken,
		})
	})

	mux.HandleFunc("PATCH /v1/transport/session/role", func(w http.ResponseWriter, r *http.Request) {
		var req UpdateTransportSessionRoleRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if strings.TrimSpace(req.InstanceID) == "" || strings.TrimSpace(req.AttachmentID) == "" || strings.TrimSpace(req.Role) == "" {
			writeErrorMessage(w, http.StatusBadRequest, "instanceId, attachmentId, and role are required")
			return
		}
		if !validTransportRole(req.Role) {
			writeErrorMessage(w, http.StatusBadRequest, "role must be owner, participant, or observer")
			return
		}
		if deps.TransportStore != nil {
			if adapter, ok := deps.TransportStore.Get(req.InstanceID + ":" + req.AttachmentID); ok {
				adapter.SetRole(req.Role)
			}
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("DELETE /v1/transport/session", func(w http.ResponseWriter, r *http.Request) {
		instanceID := strings.TrimSpace(r.URL.Query().Get("instanceId"))
		attachmentID := strings.TrimSpace(r.URL.Query().Get("attachmentId"))
		if instanceID == "" || attachmentID == "" {
			writeErrorMessage(w, http.StatusBadRequest, "instanceId and attachmentId are required")
			return
		}
		if deps.TransportStore != nil {
			deps.TransportStore.StopAndDelete(instanceID + ":" + attachmentID)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /v1/transport/offer", func(w http.ResponseWriter, r *http.Request) {
		var req TransportOfferRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if deps.TransportStore == nil {
			writeErrorMessage(w, http.StatusBadRequest, "transport store not configured")
			return
		}
		key := req.InstanceID + ":" + req.AttachmentID
		adapter, ok := deps.TransportStore.Get(key)
		if !ok {
			writeErrorMessage(w, http.StatusNotFound, "transport adapter not found for instance/attachment")
			return
		}
		answer, err := adapter.HandleOffer(req.InstanceID, req.AttachmentID, req.SdpOffer)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, TransportOfferResponse{SdpAnswer: answer})
	})

	mux.HandleFunc("POST /v1/transport/ice-candidate", func(w http.ResponseWriter, r *http.Request) {
		var req ICECandidateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErrorMessage(w, http.StatusBadRequest, "invalid json body")
			return
		}
		if deps.TransportStore == nil {
			writeErrorMessage(w, http.StatusBadRequest, "transport store not configured")
			return
		}
		key := req.InstanceID + ":" + req.AttachmentID
		adapter, ok := deps.TransportStore.Get(key)
		if !ok {
			writeErrorMessage(w, http.StatusNotFound, "transport adapter not found")
			return
		}
		if err := adapter.HandleICECandidate(req.Candidate); err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	return mux
}

func validTransportRole(role string) bool {
	switch role {
	case "owner", "participant", "observer":
		return true
	default:
		return false
	}
}

func listInstalledRuntimes(registry *runtimepkg.Registry) []runtimepkg.AdapterInfo {
	if registry == nil {
		return []runtimepkg.AdapterInfo{}
	}
	return registry.ListAdapters()
}

func writeJSON(w http.ResponseWriter, statusCode int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, statusCode int, err error) {
	writeErrorMessage(w, statusCode, err.Error())
}

func writeErrorMessage(w http.ResponseWriter, statusCode int, message string) {
	writeJSON(w, statusCode, map[string]string{"error": message})
}
