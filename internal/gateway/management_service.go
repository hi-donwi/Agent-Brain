package gateway

import (
	"net/http"

	"github.com/hi-donwi/Agent-Brain/internal/service"
)

type managementServiceStatus struct {
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
	ServiceType string `json:"service_type"`
	Version     string `json:"version,omitempty"`
	Profile     string `json:"profile,omitempty"`
}

func (s *Server) handleManagementServiceStatus(w http.ResponseWriter, _ *http.Request) {
	status := s.cfg.ServiceStatus
	if status == nil {
		status = func() (*service.StatusResult, error) { return service.Status(service.Options{}) }
	}
	result, err := status()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain OS service status is unavailable", "api_error", "service_status_unavailable")
		return
	}
	writeJSON(w, managementServiceStatus{
		Installed: result.Installed, Running: result.Running, ServiceType: result.ServiceType,
		Version: result.Version, Profile: result.Profile,
	})
}
