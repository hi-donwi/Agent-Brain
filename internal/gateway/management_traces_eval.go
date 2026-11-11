package gateway

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/evalrunner"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/trace"
)

const (
	managementEvalSuiteID  = "local-smoke"
	managementEvalTimeout  = 60 * time.Second
	managementEvalMaxToken = 32
)

type managementEvalJob struct {
	id           string
	modelID      string
	status       string
	errorCode    string
	passed       bool
	promptTokens int
	evalTokens   int
	durationMs   int64
	assertions   []map[string]any
	createdAt    time.Time
	updatedAt    time.Time
	expiresAt    time.Time
	cancel       context.CancelFunc
}

func traceManagementID(master, sessionID string) string {
	mac := hmac.New(sha256.New, []byte(master))
	_, _ = mac.Write([]byte("brain-trace-inventory-v1\x00" + sessionID))
	return "trace-" + hex.EncodeToString(mac.Sum(nil)[:12])
}

func (s *Server) handleManagementTraceSummaries(w http.ResponseWriter, _ *http.Request) {
	layout, err := home.Default()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain trace store is unavailable", "api_error", "trace_store_unavailable")
		return
	}
	summaries, err := trace.NewStore(layout.Traces).ListSessions()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain trace summaries are unavailable", "api_error", "trace_store_unavailable")
		return
	}
	items := make([]map[string]any, 0, len(summaries))
	for _, summary := range summaries {
		items = append(items, map[string]any{
			"id": traceManagementID(s.cfg.Token, summary.SessionID), "record_count": summary.RecordCount,
			"first_timestamp": summary.FirstTimestamp.UTC(), "last_timestamp": summary.LastTimestamp.UTC(),
			"models": summary.Models, "total_prompt_tokens": summary.TotalPromptToks,
			"total_eval_tokens": summary.TotalEvalToks, "total_duration_ms": summary.TotalDurationMs,
			"has_errors": summary.HasErrors,
		})
	}
	writeJSON(w, map[string]any{"sessions": items})
}

func (s *Server) handleManagementEvaluationSuites(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"suites": []map[string]any{{
		"id": managementEvalSuiteID, "name": "Local model smoke test",
		"description":       "Send one fixed synthetic prompt to an installed local catalog model and check for the READY marker.",
		"prompt_tokens_max": managementEvalMaxToken, "timeout_seconds": 45,
	}}})
}

type managementStartEvaluation struct {
	SuiteID      string `json:"suite_id"`
	ModelID      string `json:"model_id"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) managementLocalEvalModel(ctx context.Context, modelID string, config *home.Config) (string, error) {
	if s.cfg.Adapter == nil || s.cfg.Adapter.Name() != "ollama" {
		return "", errors.New("evaluation requires the local Ollama adapter")
	}
	backendID, backendConfig := config.PrimaryBackend()
	if backendID != s.runningBackendID() {
		return "", errors.New("configured backend differs from the running adapter")
	}
	endpoint, err := url.Parse(backendConfig.Endpoint)
	if err != nil || !isLoopbackHost(endpoint.Hostname()) || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", errors.New("evaluation requires a loopback backend")
	}
	model, ok := s.cfg.Catalog.GetModel(modelID)
	if !ok {
		return "", errors.New("evaluation model must be in the embedded catalog")
	}
	chatSupported := false
	for _, role := range model.Roles {
		if role == catalog.RoleChat {
			chatSupported = true
			break
		}
	}
	tag := model.Backends["ollama"]
	if !chatSupported || tag == "" {
		return "", errors.New("evaluation model is not available for local chat")
	}
	installed, err := s.cfg.Adapter.ListModels(ctx)
	if err != nil {
		return "", errors.New("local model inventory is unavailable")
	}
	found := false
	for _, item := range installed {
		if catalogModelMatches(item.Name, model.ID, tag) {
			found = true
			break
		}
	}
	if !found {
		return "", errors.New("evaluation model must be installed before use")
	}
	return tag, nil
}

func (s *Server) handleManagementStartEvaluation(w http.ResponseWriter, r *http.Request) {
	var input managementStartEvaluation
	if err := decodeManagementJSON(w, r, &input); err != nil || input.SuiteID != managementEvalSuiteID || input.ModelID == "" || input.Confirmation != "eval "+input.ModelID {
		writeAPIError(w, http.StatusBadRequest, "evaluation requires the fixed suite, catalog model ID, and exact confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeModelPullJobs()) > 0 || len(s.activeKnowledgeJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for the active Brain runtime job before evaluating a model", "invalid_request_error", "runtime_job_in_progress")
		return
	}
	if s.cfg.Residency == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain model residency state is unavailable", "api_error", "residency_unavailable")
		return
	}
	config, err := s.loadManagementRuntimeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain runtime configuration is unavailable", "api_error", "runtime_unavailable")
		return
	}
	checkCtx, checkCancel := context.WithTimeout(r.Context(), 5*time.Second)
	modelTag, err := s.managementLocalEvalModel(checkCtx, input.ModelID, config)
	checkCancel()
	if err != nil {
		writeAPIError(w, http.StatusConflict, "evaluation requires an installed catalog model on the local Ollama backend", "invalid_request_error", "local_model_required")
		return
	}
	s.evalJobsMu.Lock()
	s.pruneManagementEvalJobsLocked()
	if len(s.evalJobs) >= 32 {
		s.evalJobsMu.Unlock()
		writeAPIError(w, http.StatusTooManyRequests, "evaluation job history is full; retry after older jobs expire", "api_error", "evaluation_history_full")
		return
	}
	for _, job := range s.evalJobs {
		if isActiveEvalStatus(job.status) {
			s.evalJobsMu.Unlock()
			writeAPIError(w, http.StatusTooManyRequests, "another local evaluation is active", "api_error", "evaluation_job_limit")
			return
		}
	}
	id, err := newManagementLoginID()
	if err != nil {
		s.evalJobsMu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "evaluation could not be started", "api_error", "evaluation_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), managementEvalTimeout)
	now := time.Now().UTC()
	job := &managementEvalJob{id: id, modelID: input.ModelID, status: "queued", createdAt: now, updatedAt: now, expiresAt: now.Add(managementEvalTimeout), cancel: cancel}
	s.evalJobs[id] = job
	s.evalJobsWG.Add(1)
	s.evalJobsMu.Unlock()
	go func() {
		defer s.evalJobsWG.Done()
		s.runManagementEvaluation(ctx, job, modelTag)
	}()
	w.Header().Set("Location", "/manage/v1/evaluation-jobs/"+id)
	s.writeManagementEvalJob(w, http.StatusAccepted, job)
}

func (s *Server) runManagementEvaluation(ctx context.Context, job *managementEvalJob, modelTag string) {
	defer job.cancel()
	release, err := s.cfg.Residency.Acquire(ctx, modelTag, 0)
	if err != nil {
		status, code := "failed", "residency_unavailable"
		if errors.Is(ctx.Err(), context.Canceled) {
			status, code = "cancelled", ""
		}
		s.finishManagementEval(job, status, code)
		return
	}
	defer release()
	s.updateManagementEval(job, func(current *managementEvalJob) { current.status = "running" })
	spec := &evalrunner.TaskSpec{
		ID: managementEvalSuiteID, Name: "Local model smoke test", Model: job.modelID,
		TimeoutSeconds: 45, MaxTokens: managementEvalMaxToken,
		Prompt:     "Reply with exactly this token: READY",
		Assertions: evalrunner.SpecAssertions{OutputContains: []string{"READY"}},
	}
	result, err := evalrunner.NewRunner(s.cfg.Adapter, job.modelID).Run(ctx, spec)
	if errors.Is(ctx.Err(), context.Canceled) {
		s.finishManagementEval(job, "cancelled", "")
		return
	}
	if err != nil || result == nil {
		s.finishManagementEval(job, "failed", "evaluation_failed")
		return
	}
	assertions := make([]map[string]any, 0, len(result.Assertions))
	for _, assertion := range result.Assertions {
		assertions = append(assertions, map[string]any{"type": assertion.Type, "passed": assertion.Passed})
	}
	s.updateManagementEval(job, func(current *managementEvalJob) {
		current.status = "completed"
		current.passed = result.Passed
		current.promptTokens = result.PromptTokens
		current.evalTokens = result.EvalTokens
		current.durationMs = result.DurationMs
		current.assertions = assertions
		if !result.Passed {
			current.errorCode = "evaluation_failed"
		}
		current.expiresAt = time.Now().UTC().Add(5 * time.Minute)
	})
}

func isActiveEvalStatus(status string) bool {
	return status == "queued" || status == "running" || status == "canceling"
}

func (s *Server) activeManagementEvalJobs() []managementEvalJob {
	s.evalJobsMu.Lock()
	defer s.evalJobsMu.Unlock()
	var active []managementEvalJob
	for _, job := range s.evalJobs {
		if isActiveEvalStatus(job.status) {
			active = append(active, *job)
		}
	}
	return active
}

func (s *Server) updateManagementEval(job *managementEvalJob, update func(*managementEvalJob)) {
	s.evalJobsMu.Lock()
	defer s.evalJobsMu.Unlock()
	update(job)
	job.updatedAt = time.Now().UTC()
}

func (s *Server) finishManagementEval(job *managementEvalJob, status, errorCode string) {
	s.updateManagementEval(job, func(current *managementEvalJob) {
		current.status = status
		current.errorCode = errorCode
		current.expiresAt = time.Now().UTC().Add(5 * time.Minute)
	})
}

func (s *Server) pruneManagementEvalJobsLocked() {
	now := time.Now()
	for id, job := range s.evalJobs {
		if !job.expiresAt.After(now) {
			if job.cancel != nil {
				job.cancel()
			}
			delete(s.evalJobs, id)
		}
	}
}

func managementEvalJobResponse(job *managementEvalJob) map[string]any {
	return map[string]any{
		"id": job.id, "suite_id": managementEvalSuiteID, "model_id": job.modelID, "status": job.status,
		"error_code": job.errorCode, "passed": job.passed, "prompt_tokens": job.promptTokens,
		"eval_tokens": job.evalTokens, "duration_ms": job.durationMs, "assertions": job.assertions,
		"created_at": job.createdAt.Format(time.RFC3339), "updated_at": job.updatedAt.Format(time.RFC3339),
	}
}

func (s *Server) writeManagementEvalJob(w http.ResponseWriter, status int, job *managementEvalJob) {
	s.evalJobsMu.Lock()
	copy := *job
	s.evalJobsMu.Unlock()
	writeJSONStatus(w, status, managementEvalJobResponse(&copy))
}

func (s *Server) handleManagementEvaluationJobs(w http.ResponseWriter, _ *http.Request) {
	s.evalJobsMu.Lock()
	s.pruneManagementEvalJobsLocked()
	jobs := make([]managementEvalJob, 0, len(s.evalJobs))
	for _, job := range s.evalJobs {
		jobs = append(jobs, *job)
	}
	s.evalJobsMu.Unlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].createdAt.After(jobs[j].createdAt) })
	items := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		items = append(items, managementEvalJobResponse(&jobs[i]))
	}
	writeJSON(w, map[string]any{"jobs": items})
}

func (s *Server) managementEvalJob(id string) *managementEvalJob {
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		return nil
	}
	s.evalJobsMu.Lock()
	defer s.evalJobsMu.Unlock()
	s.pruneManagementEvalJobsLocked()
	job := s.evalJobs[id]
	if job == nil {
		return nil
	}
	copy := *job
	return &copy
}

func (s *Server) handleManagementEvaluationStatus(w http.ResponseWriter, r *http.Request) {
	job := s.managementEvalJob(r.PathValue("id"))
	if job == nil {
		writeAPIError(w, http.StatusNotFound, "evaluation job not found", "invalid_request_error", "not_found")
		return
	}
	writeJSON(w, managementEvalJobResponse(job))
}

func (s *Server) handleManagementCancelEvaluation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusNotFound, "evaluation job not found", "invalid_request_error", "not_found")
		return
	}
	s.evalJobsMu.Lock()
	job := s.evalJobs[id]
	if job == nil {
		s.evalJobsMu.Unlock()
		writeAPIError(w, http.StatusNotFound, "evaluation job not found", "invalid_request_error", "not_found")
		return
	}
	if isActiveEvalStatus(job.status) {
		job.status = "canceling"
		job.updatedAt = time.Now().UTC()
		if job.cancel != nil {
			job.cancel()
		}
	}
	copy := *job
	s.evalJobsMu.Unlock()
	s.writeManagementEvalJob(w, http.StatusAccepted, &copy)
}
