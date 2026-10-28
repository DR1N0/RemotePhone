// Package serverinfo owns the control plane's stable server identifier and
// user-facing name. The identifier is generated once and persisted next to
// the database; the name is deployment configuration and may change without
// invalidating paired clients (which key on the identifier and certificate).
package serverinfo

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"remotephone/internal/controlplane/apiserver/middleware"
)

// Info is the small, unauthenticated identity document used by client
// discovery and by the app's server picker.
type Info struct {
	ServerID string `json:"serverId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
}

// LoadOrCreate loads the server ID from idPath, generating and persisting a
// UUID on first run. name is trimmed; an empty name falls back to the host's
// hostname (and finally to "remotephone").
func LoadOrCreate(idPath, name string) (*Info, error) {
	if strings.TrimSpace(idPath) == "" {
		return nil, fmt.Errorf("server ID path must not be empty")
	}
	idBytes, err := os.ReadFile(idPath)
	var serverID string
	switch {
	case err == nil:
		serverID = strings.TrimSpace(string(idBytes))
		if serverID == "" {
			return nil, fmt.Errorf("server ID file %s is empty", idPath)
		}
	case os.IsNotExist(err):
		serverID = uuid.NewString()
		if err := os.MkdirAll(filepath.Dir(idPath), 0700); err != nil {
			return nil, fmt.Errorf("create server ID directory: %w", err)
		}
		if err := os.WriteFile(idPath, []byte(serverID+"\n"), 0600); err != nil {
			return nil, fmt.Errorf("write server ID: %w", err)
		}
	default:
		return nil, fmt.Errorf("read server ID: %w", err)
	}

	if strings.TrimSpace(name) == "" {
		name, _ = os.Hostname()
	}
	if strings.TrimSpace(name) == "" {
		name = "remotephone"
	}
	return &Info{
		ServerID: serverID,
		Name:     strings.TrimSpace(name),
		Version:  "1",
	}, nil
}

// RegisterRoutes mounts the unauthenticated server identity endpoint.
func RegisterRoutes(mux *http.ServeMux, info *Info) {
	if info == nil {
		return
	}
	mux.HandleFunc("GET /api/v1/server/info", func(w http.ResponseWriter, r *http.Request) {
		middleware.WriteJSON(w, http.StatusOK, map[string]any{
			"serverId": info.ServerID,
			"name":     info.Name,
			"version":  info.Version,
		})
	})
}
