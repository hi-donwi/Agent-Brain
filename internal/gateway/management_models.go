package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/hw"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/hi-donwi/Agent-Brain/internal/setup"
)

const (
	maxActiveModelPullJobs = 1
	modelPullTimeout       = 6 * time.Hour
)

type managementModelPullJob struct {
	id        string
	modelID   string
	backendID string
	status    string
	errorCode string
	completed uint64
	total     uint64
	createdAt time.Time
	updatedAt time.Time
	expiresAt time.Time
	cancel    context.CancelFunc
}

type startModelPullRequest struct {
	CatalogModelID string `json:"catalog_model_id"`
	Confirmation   string `json:"confirmation"`
}

func (s *Server) handleManagementStartModelPull(w http.ResponseWriter, r *http.Request) {
	var input startModelPullRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.CatalogModelID == "" || input.Confirmation != input.CatalogModelID {
		writeAPIError(w, http.StatusBadRequest, "model pull requires an exact catalog confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeKnowledgeJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for active knowledge/evaluation work before pulling a model", "invalid_request_error", "runtime_job_in_progress")
		return
	}
	if !s.supportsModelManagement() || s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusConflict, "the running backend does not support local model management", "invalid_request_error", "model_management_unsupported")
		return
	}
	config, err := s.loadManagementRuntimeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	configuredID, _ := config.PrimaryBackend()
	if configuredID != s.runningBackendID() {
		writeAPIError(w, http.StatusConflict, "restart Brain to apply the selected backend before pulling a model", "invalid_request_error", "gateway_restart_required")
		return
	}
	model, ok := s.cfg.Catalog.GetModel(input.CatalogModelID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "model is not in the embedded catalog", "invalid_request_error", "catalog_model_not_found")
		return
	}
	backendTag := model.Backends[s.cfg.Adapter.Name()]
	if backendTag == "" {
		writeAPIError(w, http.StatusConflict, "model is not available for the running backend", "invalid_request_error", "model_backend_unsupported")
		return
	}
	checkCtx, checkCancel := context.WithTimeout(r.Context(), 5*time.Second)
	installed, err := s.cfg.Adapter.ListModels(checkCtx)
	checkCancel()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "installed models are unavailable from the running backend", "api_error", "backend_unavailable")
		return
	}
	for _, item := range installed {
		if catalogModelMatches(item.Name, model.ID, backendTag) {
			writeAPIError(w, http.StatusConflict, "model is already installed", "invalid_request_error", "model_already_installed")
			return
		}
	}
	freeBytes := s.cfg.RuntimeFreeBytes
	if freeBytes == nil {
		freeBytes = hw.FreeBytes
	}
	free, err := freeBytes(filepath.Dir(s.cfg.ConfigPath))
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "free disk space could not be checked", "api_error", "disk_check_failed")
		return
	}
	required := model.DownloadBytes + setup.DefaultSafetyMarginBytes
	if required < model.DownloadBytes || free < required {
		writeAPIError(w, http.StatusConflict, "insufficient free disk space for this model and the required safety margin", "invalid_request_error", "insufficient_disk")
		return
	}
	if s.cfg.ConfigPath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	s.pruneManagementModelJobs()
	s.modelJobsMu.Lock()
	active := 0
	for _, job := range s.modelJobs {
		if isActiveModelJob(job.status) {
			active++
		}
	}
	if len(s.modelJobs) >= 32 {
		s.modelJobsMu.Unlock()
		writeAPIError(w, http.StatusTooManyRequests, "model download history is full; retry after older jobs expire", "api_error", "model_pull_history_full")
		return
	}
	if active >= maxActiveModelPullJobs {
		s.modelJobsMu.Unlock()
		writeAPIError(w, http.StatusTooManyRequests, "another model download is already active", "api_error", "model_pull_limit")
		return
	}
	id, err := newManagementLoginID()
	if err != nil {
		s.modelJobsMu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "model download could not be started", "api_error", "model_pull_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), modelPullTimeout)
	now := time.Now().UTC()
	job := &managementModelPullJob{
		id: id, modelID: model.ID, backendID: s.runningBackendID(), status: "queued",
		total: model.DownloadBytes, createdAt: now, updatedAt: now, expiresAt: now.Add(modelPullTimeout), cancel: cancel,
	}
	s.modelJobs[id] = job
	s.modelJobsWG.Add(1)
	s.modelJobsMu.Unlock()
	go func() {
		defer s.modelJobsWG.Done()
		s.runManagementModelPull(ctx, job, backendTag)
	}()
	w.Header().Set("Location", "/manage/v1/model-pull-jobs/"+id)
	s.writeModelPullJob(w, http.StatusAccepted, job)
}

func (s *Server) runManagementModelPull(ctx context.Context, job *managementModelPullJob, backendTag string) {
	defer job.cancel()
	s.updateModelPullJob(job, func(current *managementModelPullJob) { current.status = "running" })
	err := s.cfg.Adapter.Pull(ctx, backendTag, func(progress backend.Progress) {
		s.updateModelPullJob(job, func(current *managementModelPullJob) {
			current.completed = progress.Completed
			if progress.Total > 0 {
				current.total = progress.Total
				if current.completed > current.total {
					current.completed = current.total
				}
			}
			if current.completed > current.total {
				current.completed = current.total
			}
		})
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			s.finishModelPullJob(job, "cancelled", "")
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.finishModelPullJob(job, "failed", "model_pull_timeout")
		} else {
			s.finishModelPullJob(job, "failed", "backend_pull_failed")
		}
		return
	}
	s.updateModelPullJob(job, func(current *managementModelPullJob) {
		current.status = "completed"
		current.completed = current.total
		current.expiresAt = time.Now().UTC().Add(5 * time.Minute)
	})
}

func (s *Server) updateModelPullJob(job *managementModelPullJob, update func(*managementModelPullJob)) {
	s.modelJobsMu.Lock()
	defer s.modelJobsMu.Unlock()
	update(job)
	job.updatedAt = time.Now().UTC()
}

func (s *Server) finishModelPullJob(job *managementModelPullJob, status, errorCode string) {
	s.updateModelPullJob(job, func(current *managementModelPullJob) {
		current.status = status
		current.errorCode = errorCode
		current.expiresAt = time.Now().UTC().Add(5 * time.Minute)
	})
}

func isActiveModelJob(status string) bool {
	return status == "queued" || status == "running" || status == "canceling"
}

func (s *Server) pruneManagementModelJobs() {
	now := time.Now()
	s.modelJobsMu.Lock()
	defer s.modelJobsMu.Unlock()
	for id, job := range s.modelJobs {
		if !job.expiresAt.After(now) {
			if job.cancel != nil {
				job.cancel()
			}
			delete(s.modelJobs, id)
		}
	}
}

func (s *Server) managementModelJob(id string) *managementModelPullJob {
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		return nil
	}
	s.pruneManagementModelJobs()
	s.modelJobsMu.Lock()
	defer s.modelJobsMu.Unlock()
	job := s.modelJobs[id]
	if job == nil {
		return nil
	}
	copy := *job
	return &copy
}

func (s *Server) handleManagementModelPullStatus(w http.ResponseWriter, r *http.Request) {
	job := s.managementModelJob(r.PathValue("id"))
	if job == nil {
		writeAPIError(w, http.StatusNotFound, "model pull job not found", "invalid_request_error", "not_found")
		return
	}
	s.writeModelPullJob(w, http.StatusOK, job)
}

func (s *Server) handleManagementModelPullJobs(w http.ResponseWriter, _ *http.Request) {
	s.pruneManagementModelJobs()
	s.modelJobsMu.Lock()
	jobs := make([]managementModelPullJob, 0, len(s.modelJobs))
	for _, job := range s.modelJobs {
		jobs = append(jobs, *job)
	}
	s.modelJobsMu.Unlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].createdAt.After(jobs[j].createdAt) })
	items := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		items = append(items, modelPullJobResponse(&jobs[i]))
	}
	writeJSON(w, map[string]any{"jobs": items})
}

func (s *Server) handleManagementCancelModelPull(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusNotFound, "model pull job not found", "invalid_request_error", "not_found")
		return
	}
	s.pruneManagementModelJobs()
	s.modelJobsMu.Lock()
	job := s.modelJobs[id]
	if job == nil {
		s.modelJobsMu.Unlock()
		writeAPIError(w, http.StatusNotFound, "model pull job not found", "invalid_request_error", "not_found")
		return
	}
	if isActiveModelJob(job.status) {
		job.status = "canceling"
		job.updatedAt = time.Now().UTC()
		if job.cancel != nil {
			job.cancel()
		}
	}
	copy := *job
	s.modelJobsMu.Unlock()
	s.writeModelPullJob(w, http.StatusAccepted, &copy)
}

func (s *Server) writeModelPullJob(w http.ResponseWriter, status int, job *managementModelPullJob) {
	s.modelJobsMu.Lock()
	copy := *job
	s.modelJobsMu.Unlock()
	writeJSONStatus(w, status, modelPullJobResponse(&copy))
}

func modelPullJobResponse(job *managementModelPullJob) map[string]any {
	return map[string]any{
		"id": job.id, "catalog_model_id": job.modelID, "backend_id": job.backendID,
		"status": job.status, "completed_bytes": job.completed, "total_bytes": job.total,
		"error_code": job.errorCode, "created_at": job.createdAt.Format(time.RFC3339),
		"updated_at": job.updatedAt.Format(time.RFC3339),
	}
}

func writeJSONStatus(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type deleteModelRequest struct {
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementDeleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid catalog model ID", "invalid_request_error", "bad_request")
		return
	}
	var input deleteModelRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "delete "+id {
		writeAPIError(w, http.StatusBadRequest, "model deletion requires the exact catalog ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeKnowledgeJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for active knowledge/evaluation work before deleting a model", "invalid_request_error", "runtime_job_in_progress")
		return
	}
	if !s.supportsModelManagement() || s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusConflict, "the running backend does not support local model management", "invalid_request_error", "model_management_unsupported")
		return
	}
	config, err := s.loadManagementRuntimeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	configuredID, _ := config.PrimaryBackend()
	if configuredID != s.runningBackendID() {
		writeAPIError(w, http.StatusConflict, "restart Brain to apply the selected backend before removing a model", "invalid_request_error", "gateway_restart_required")
		return
	}
	model, ok := s.cfg.Catalog.GetModel(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "model is not in the embedded catalog", "invalid_request_error", "catalog_model_not_found")
		return
	}
	backendTag := model.Backends[s.cfg.Adapter.Name()]
	if backendTag == "" {
		writeAPIError(w, http.StatusConflict, "model is not available for the running backend", "invalid_request_error", "model_backend_unsupported")
		return
	}
	if s.modelIsRouted(config, model, backendTag) {
		writeAPIError(w, http.StatusConflict, "model is assigned to an active role/account; change the model profile before removing it", "invalid_request_error", "model_in_use")
		return
	}
	if len(s.activeModelPullJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for the active model download before removing a model", "invalid_request_error", "model_pull_in_progress")
		return
	}
	mutationCtx, mutationCancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer mutationCancel()
	if s.cfg.Residency == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "model usage state is unavailable", "api_error", "residency_unavailable")
		return
	}
	release, err := s.cfg.Residency.BlockModel(mutationCtx, backendTag)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "model has active requests or another mutation is in progress", "invalid_request_error", "model_mutation_unavailable")
		return
	}
	defer release()
	installed, err := s.cfg.Adapter.ListModels(mutationCtx)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "installed models are unavailable from the running backend", "api_error", "backend_unavailable")
		return
	}
	var installedName string
	for _, item := range installed {
		if catalogModelMatches(item.Name, model.ID, backendTag) {
			installedName = item.Name
			break
		}
	}
	if installedName == "" {
		writeAPIError(w, http.StatusNotFound, "model is not installed", "invalid_request_error", "model_not_installed")
		return
	}
	running, err := s.cfg.Adapter.RunningModels(mutationCtx)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "running model usage could not be checked", "api_error", "backend_unavailable")
		return
	}
	for _, item := range running {
		if item.Name == installedName || catalogModelMatches(item.Name, model.ID, backendTag) {
			writeAPIError(w, http.StatusConflict, "model is currently loaded; unload it before removal", "invalid_request_error", "model_running")
			return
		}
	}
	err = s.cfg.Adapter.Delete(mutationCtx, installedName)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "backend could not remove the model", "api_error", "model_delete_failed")
		return
	}
	if err := s.cfg.Residency.ForgetBlockedModel(backendTag); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "model was removed but residency accounting could not be reconciled", "api_error", "residency_update_failed")
		return
	}
	writeJSON(w, map[string]any{"catalog_model_id": id, "removed": true, "restart_required": false})
}

type unloadModelRequest struct {
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementUnloadModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusBadRequest, "invalid catalog model ID", "invalid_request_error", "bad_request")
		return
	}
	var input unloadModelRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "unload "+id {
		writeAPIError(w, http.StatusBadRequest, "model unload requires the exact catalog ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeKnowledgeJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for active knowledge/evaluation work before unloading a model", "invalid_request_error", "runtime_job_in_progress")
		return
	}
	if !s.supportsModelManagement() || s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusConflict, "the running backend does not support local model management", "invalid_request_error", "model_management_unsupported")
		return
	}
	if len(s.activeModelPullJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for the active model download before unloading a model", "invalid_request_error", "model_pull_in_progress")
		return
	}
	model, ok := s.cfg.Catalog.GetModel(id)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "model is not in the embedded catalog", "invalid_request_error", "catalog_model_not_found")
		return
	}
	backendTag := model.Backends[s.cfg.Adapter.Name()]
	if backendTag == "" {
		writeAPIError(w, http.StatusConflict, "model is not available for the running backend", "invalid_request_error", "model_backend_unsupported")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	release, err := s.cfg.Residency.BlockModel(ctx, backendTag)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "model has active requests or another mutation is in progress", "invalid_request_error", "model_mutation_unavailable")
		return
	}
	defer release()
	running, err := s.cfg.Adapter.RunningModels(ctx)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "running model usage could not be checked", "api_error", "backend_unavailable")
		return
	}
	loaded := false
	for _, item := range running {
		if item.Name == backendTag || catalogModelMatches(item.Name, model.ID, backendTag) {
			loaded = true
			break
		}
	}
	if !loaded {
		writeAPIError(w, http.StatusConflict, "model is not loaded in the backend", "invalid_request_error", "model_not_loaded")
		return
	}
	if err := s.cfg.Adapter.Unload(ctx, backendTag); err != nil {
		writeAPIError(w, http.StatusBadGateway, "backend could not unload the model", "api_error", "model_unload_failed")
		return
	}
	if err := s.cfg.Residency.ForgetBlockedModel(backendTag); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "model was unloaded but residency accounting could not be reconciled", "api_error", "residency_update_failed")
		return
	}
	writeJSON(w, map[string]any{"catalog_model_id": id, "unloaded": true})
}

func (s *Server) modelIsRouted(config *home.Config, model catalog.Model, backendTag string) bool {
	for _, alias := range config.Aliases {
		if alias == model.ID || alias == backendTag {
			return true
		}
	}
	profiles := s.cfg.RoutingProfiles
	accounts := []router.Account(nil)
	if s.cfg.AccountConfigPath != "" {
		current, err := router.LoadRouterConfig(s.cfg.AccountConfigPath)
		if err == nil {
			profiles, accounts = current.RoutingProfiles, current.Accounts
		} else if !errors.Is(err, os.ErrNotExist) {
			return true
		}
	}
	for _, profile := range profiles {
		for _, target := range profile.Targets {
			if router.NormalizeProvider(target.Provider) == router.ProviderLocal && (target.UpstreamModel == model.ID || target.UpstreamModel == backendTag) {
				return true
			}
		}
	}
	for _, account := range accounts {
		if router.NormalizeProvider(account.Provider) != router.ProviderLocal {
			continue
		}
		for _, id := range append(append([]string(nil), account.Models...), account.DiscoveredModels...) {
			if id == model.ID || id == backendTag {
				return true
			}
		}
	}
	return false
}

func (s *Server) activeModelPullJobs() []managementModelPullJob {
	s.modelJobsMu.Lock()
	defer s.modelJobsMu.Unlock()
	var active []managementModelPullJob
	for _, job := range s.modelJobs {
		if isActiveModelJob(job.status) {
			active = append(active, *job)
		}
	}
	return active
}
