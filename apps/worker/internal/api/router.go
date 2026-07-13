package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	runtimepkg "mako-worker/internal/runtime"
)

type Dependencies struct {
	WorkerID        string
	ListenBaseURL   string
	HostInventory   HostInventory
	RuntimeRegistry *runtimepkg.Registry
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

		transportSession, err := deps.RuntimeRegistry.CreateTransportSession(runtimepkg.TransportSessionRequest{
			SessionID:     req.SessionID,
			AttachmentID:  req.AttachmentID,
			InstanceID:    req.InstanceID,
			RuntimeClass:  req.RuntimeClass,
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

	return mux
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
