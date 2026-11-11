package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/hw"
)

type managementBackend struct {
	ID              string `json:"id"`
	Endpoint        string `json:"endpoint"`
	Enabled         bool   `json:"enabled"`
	TokenConfigured bool   `json:"token_configured"`
}

type managementHardware struct {
	OS          string   `json:"os"`
	Arch        string   `json:"arch"`
	CPU         string   `json:"cpu"`
	LogicalCPUs int      `json:"logical_cpus"`
	MemoryBytes uint64   `json:"memory_bytes"`
	Accelerator string   `json:"accelerator"`
	GPUs        []hw.GPU `json:"gpus,omitempty"`
}

type managementRuntime struct {
	Hardware            managementHardware  `json:"hardware"`
	Profile             string              `json:"profile"`
	Aliases             map[string]string   `json:"aliases"`
	FreeDiskBytes       uint64              `json:"free_disk_bytes"`
	FreeDiskKnown       bool                `json:"free_disk_known"`
	Backends            []managementBackend `json:"backends"`
	ConfiguredBackendID string              `json:"configured_backend_id"`
	RunningBackendID    string              `json:"running_backend_id,omitempty"`
	BackendReachable    bool                `json:"backend_reachable"`
	BackendVersion      string              `json:"backend_version,omitempty"`
	RestartRequired     bool                `json:"restart_required"`
	ModelManagement     bool                `json:"model_management_supported"`
}

type managementModel struct {
	Name       string         `json:"name"`
	Size       uint64         `json:"size"`
	Digest     string         `json:"digest,omitempty"`
	ModifiedAt time.Time      `json:"modified_at,omitempty"`
	CatalogID  string         `json:"catalog_id,omitempty"`
	Roles      []catalog.Role `json:"roles,omitempty"`
	Running    bool           `json:"running"`
}

func (s *Server) loadManagementRuntimeConfig() (*home.Config, error) {
	if s.cfg.ConfigPath == "" {
		return nil, errors.New("runtime configuration is not available")
	}
	return home.LoadConfig(s.cfg.ConfigPath)
}

func (s *Server) handleManagementRuntime(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadManagementRuntimeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	detect := s.cfg.RuntimeDetectHardware
	if detect == nil {
		detect = hw.Detect
	}
	info, err := detect()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain host hardware information is unavailable", "api_error", "runtime_unavailable")
		return
	}
	freeBytes := uint64(0)
	freeDiskKnown := false
	freeBytesFunc := s.cfg.RuntimeFreeBytes
	if freeBytesFunc == nil {
		freeBytesFunc = hw.FreeBytes
	}
	if bytes, err := freeBytesFunc(filepath.Dir(s.cfg.ConfigPath)); err == nil {
		freeBytes = bytes
		freeDiskKnown = true
	}
	configuredID, _ := config.PrimaryBackend()
	backends := make([]managementBackend, 0, len(config.Backends))
	for id, backendConfig := range config.Backends {
		backends = append(backends, managementBackend{
			ID: id, Endpoint: redactBackendEndpoint(backendConfig.Endpoint), Enabled: backendConfig.Enabled, TokenConfigured: backendConfig.Token != "",
		})
	}
	sort.Slice(backends, func(i, j int) bool { return backends[i].ID < backends[j].ID })
	hardware := managementHardware{
		OS: info.OS, Arch: info.Arch, CPU: info.CPU.Model, LogicalCPUs: info.CPU.Logical,
		MemoryBytes: info.Memory.TotalBytes, Accelerator: info.Accelerator, GPUs: info.GPUs,
	}
	runtime := managementRuntime{
		Hardware: hardware, Profile: config.Profile, Aliases: config.Aliases, FreeDiskBytes: freeBytes, FreeDiskKnown: freeDiskKnown,
		Backends: backends, ConfiguredBackendID: configuredID, ModelManagement: s.supportsModelManagement(),
	}
	if s.cfg.Adapter != nil {
		runtime.RunningBackendID = s.runningBackendID()
		runtime.RestartRequired = configuredID != runtime.RunningBackendID
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		if s.cfg.Adapter.Ping(ctx) == nil {
			runtime.BackendReachable = true
			if version, err := s.cfg.Adapter.Version(ctx); err == nil {
				runtime.BackendVersion = version
			}
		}
		cancel()
	}
	writeJSON(w, runtime)
}

func redactBackendEndpoint(endpoint string) string {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (s *Server) handleManagementModels(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	models, err := s.listManagementModels(ctx)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "installed models are unavailable from the running backend", "api_error", "backend_unavailable")
		return
	}
	writeJSON(w, map[string]any{"backend_id": s.runningBackendID(), "models": models})
}

func (s *Server) handleManagementModelCatalog(w http.ResponseWriter, _ *http.Request) {
	if s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "no inference backend is loaded", "api_error", "backend_unavailable")
		return
	}
	backendID := s.cfg.Adapter.Name()
	models := make([]catalog.Model, 0)
	for _, model := range s.cfg.Catalog.Models {
		if model.Backends[backendID] != "" {
			models = append(models, model)
		}
	}
	profiles := make([]catalog.Profile, 0)
	for _, profile := range s.cfg.Catalog.Profiles {
		usable := true
		for _, id := range profile.Defaults {
			model, ok := s.cfg.Catalog.GetModel(id)
			if !ok || model.Backends[backendID] == "" {
				usable = false
				break
			}
		}
		if usable {
			profiles = append(profiles, profile)
		}
	}
	writeJSON(w, map[string]any{"backend_id": backendID, "models": models, "profiles": profiles})
}

func (s *Server) listManagementModels(ctx context.Context) ([]managementModel, error) {
	if s.cfg.Adapter == nil {
		return nil, errors.New("backend not configured")
	}
	installed, err := s.cfg.Adapter.ListModels(ctx)
	if err != nil {
		return nil, err
	}
	running, err := s.cfg.Adapter.RunningModels(ctx)
	if err != nil {
		return nil, err
	}
	models := make([]managementModel, 0, len(installed))
	for _, item := range installed {
		model := managementModel{Name: item.Name, Size: item.Size, Digest: item.Digest, ModifiedAt: item.ModifiedAt}
		for _, active := range running {
			if active.Name == item.Name {
				model.Running = true
				break
			}
		}
		for _, entry := range s.cfg.Catalog.Models {
			if catalogModelMatches(item.Name, entry.ID, entry.Backends[s.cfg.Adapter.Name()]) {
				model.CatalogID, model.Roles = entry.ID, entry.Roles
				break
			}
		}
		models = append(models, model)
	}
	sort.Slice(models, func(i, j int) bool { return models[i].Name < models[j].Name })
	return models, nil
}

func catalogModelMatches(installed, catalogID, backendID string) bool {
	if installed == catalogID || (backendID != "" && installed == backendID) {
		return true
	}
	return backendID != "" && strings.HasPrefix(installed, backendID+":")
}

func (s *Server) supportsModelManagement() bool {
	return s.cfg.Adapter != nil && s.cfg.Adapter.Name() == "ollama"
}

func (s *Server) runningBackendID() string {
	if s.cfg.LocalBackend != "" {
		return s.cfg.LocalBackend
	}
	if s.cfg.Adapter == nil {
		return ""
	}
	return s.cfg.Adapter.Name()
}

type selectBackendRequest struct {
	BackendID    string `json:"backend_id"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementSelectBackend(w http.ResponseWriter, r *http.Request) {
	var input selectBackendRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.BackendID == "" || input.Confirmation != input.BackendID {
		writeAPIError(w, http.StatusBadRequest, "backend selection requires an exact confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeKnowledgeJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for knowledge indexing before switching backends", "invalid_request_error", "knowledge_index_in_progress")
		return
	}
	if len(s.activeModelPullJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for the active model download before switching backends", "invalid_request_error", "model_pull_in_progress")
		return
	}
	config, err := s.loadManagementRuntimeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	selected, ok := config.Backends[input.BackendID]
	if !ok {
		writeAPIError(w, http.StatusNotFound, "backend is not configured in Brain", "invalid_request_error", "backend_not_found")
		return
	}
	adapter, err := backend.NewAdapter(input.BackendID, selected.Endpoint, selected.Token, nil)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "configured backend type is unsupported", "invalid_request_error", "backend_unsupported")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	err = adapter.Ping(ctx)
	cancel()
	if err != nil {
		writeAPIError(w, http.StatusConflict, "configured backend is not reachable", "invalid_request_error", "backend_unreachable")
		return
	}
	for name, value := range config.Backends {
		value.Enabled = name == input.BackendID
		config.Backends[name] = value
	}
	if err := home.SaveConfig(s.cfg.ConfigPath, config); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "backend selection could not be saved", "api_error", "config_write_failed")
		return
	}
	writeJSON(w, map[string]any{"backend_id": input.BackendID, "restart_required": s.cfg.Adapter == nil || s.runningBackendID() != input.BackendID})
}

type selectProfileRequest struct {
	Profile      string `json:"profile"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementSelectProfile(w http.ResponseWriter, r *http.Request) {
	var input selectProfileRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Profile == "" || input.Confirmation != input.Profile {
		writeAPIError(w, http.StatusBadRequest, "profile selection requires an exact confirmation", "invalid_request_error", "bad_request")
		return
	}
	profile, ok := s.cfg.Catalog.GetProfile(catalog.ProfileName(input.Profile))
	if !ok {
		writeAPIError(w, http.StatusNotFound, "hardware profile is not in the embedded catalog", "invalid_request_error", "profile_not_found")
		return
	}
	if s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "no inference backend is loaded", "api_error", "backend_unavailable")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeKnowledgeJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for knowledge indexing before changing model profile", "invalid_request_error", "knowledge_index_in_progress")
		return
	}
	if len(s.activeModelPullJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for the active model download before changing model profile", "invalid_request_error", "model_pull_in_progress")
		return
	}
	config, err := s.loadManagementRuntimeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	configuredID, _ := config.PrimaryBackend()
	if configuredID != s.runningBackendID() {
		writeAPIError(w, http.StatusConflict, "restart Brain to apply the selected backend before changing model profile", "invalid_request_error", "gateway_restart_required")
		return
	}
	checkCtx, checkCancel := context.WithTimeout(r.Context(), 5*time.Second)
	installed, err := s.cfg.Adapter.ListModels(checkCtx)
	checkCancel()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "installed models are unavailable from the running backend", "api_error", "backend_unavailable")
		return
	}
	aliases := make(map[string]string, len(profile.Defaults))
	missing := make([]string, 0)
	for role, modelID := range profile.Defaults {
		model, ok := s.cfg.Catalog.GetModel(modelID)
		if !ok {
			continue
		}
		tag := model.Backends[s.cfg.Adapter.Name()]
		if tag == "" {
			writeAPIError(w, http.StatusConflict, "model profile is unsupported by the selected backend", "invalid_request_error", "profile_backend_unsupported")
			return
		}
		aliases[string(role)] = modelID
		found := false
		for _, item := range installed {
			if catalogModelMatches(item.Name, modelID, tag) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, modelID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		writeJSONStatus(w, http.StatusConflict, map[string]any{"error": map[string]any{"type": "invalid_request_error", "message": "profile models must be installed before selecting this profile", "code": "profile_models_missing"}, "missing_models": missing})
		return
	}
	config.Profile = string(profile.Name)
	config.Aliases = aliases
	if err := home.SaveConfig(s.cfg.ConfigPath, config); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "model profile could not be saved", "api_error", "config_write_failed")
		return
	}
	writeJSON(w, map[string]any{"profile": profile.Name, "aliases": aliases, "restart_required": true})
}
