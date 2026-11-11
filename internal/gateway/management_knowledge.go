package gateway

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/hi-donwi/Agent-Brain/internal/catalog"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/rag"
	"github.com/hi-donwi/Agent-Brain/internal/store"
)

const (
	maxActiveKnowledgeJobs   = 1
	knowledgeIndexTimeout    = 4 * time.Hour
	maxKnowledgeQueryRunes   = 4096
	maxKnowledgeSearchLimit  = 10
	maxKnowledgeSnippetRunes = 1200
)

type managementKnowledgeSource struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Collection     string `json:"collection"`
	EmbeddingModel string `json:"embedding_model"`
	RootAvailable  bool   `json:"root_available"`
	Indexed        bool   `json:"indexed"`
	Documents      int    `json:"documents"`
	Chunks         int    `json:"chunks"`
	Bytes          int64  `json:"bytes"`
}

type managementKnowledgeJob struct {
	id            string
	sourceID      string
	name          string
	collection    string
	root          string
	embedModel    string
	status        string
	errorCode     string
	filesIndexed  int
	chunksCreated int
	filesScanned  int
	filesSkipped  int
	filesPruned   int
	createdAt     time.Time
	updatedAt     time.Time
	expiresAt     time.Time
	cancel        context.CancelFunc
}

func (s *Server) loadKnowledgeConfig() (*home.Config, error) {
	return s.loadManagementRuntimeConfig()
}

func (s *Server) resolveKnowledgeSource(source home.KnowledgeSource) (string, error) {
	root := filepath.Clean(source.Root)
	if !filepath.IsAbs(source.Root) || root != source.Root {
		return "", errors.New("knowledge source root is not canonical")
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil || filepath.Clean(resolved) != root {
		return "", errors.New("knowledge source root is unavailable or changed")
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("knowledge source root is unavailable")
	}
	brainHome := filepath.Dir(s.cfg.ConfigPath)
	if resolvedHome, err := filepath.EvalSymlinks(brainHome); err == nil {
		brainHome = filepath.Clean(resolvedHome)
	} else {
		brainHome, _ = filepath.Abs(brainHome)
	}
	if knowledgePathWithin(brainHome, root) || knowledgePathWithin(root, brainHome) {
		return "", errors.New("knowledge source overlaps Brain's private home")
	}
	return root, nil
}

func findKnowledgeSource(config *home.Config, id string) (home.KnowledgeSource, bool) {
	for _, source := range config.KnowledgeSources {
		if source.ID == id {
			return source, true
		}
	}
	return home.KnowledgeSource{}, false
}

func knowledgePathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) handleManagementKnowledge(w http.ResponseWriter, r *http.Request) {
	config, err := s.loadKnowledgeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge configuration is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	if s.cfg.StorePath == "" {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge store is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	db, err := store.Open(s.cfg.StorePath)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge store is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	defer db.Close()
	summaries, err := db.ListCollectionSummaries(r.Context())
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain collection metadata is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	byName := make(map[string]store.CollectionSummary, len(summaries))
	collections := make([]map[string]any, 0, len(summaries))
	for _, summary := range summaries {
		byName[summary.Collection.Name] = summary
		collections = append(collections, map[string]any{
			"name": summary.Collection.Name, "model": summary.Collection.Model,
			"documents": summary.Documents, "chunks": summary.Chunks, "bytes": summary.Bytes,
		})
	}
	sources := make([]managementKnowledgeSource, 0, len(config.KnowledgeSources))
	for _, source := range config.KnowledgeSources {
		_, rootErr := s.resolveKnowledgeSource(source)
		item := managementKnowledgeSource{
			ID: source.ID, Name: source.Name, Collection: source.Collection,
			EmbeddingModel: source.EmbeddingModel, RootAvailable: rootErr == nil,
		}
		if summary, ok := byName[source.Collection]; ok {
			item.Indexed = true
			item.Documents = summary.Documents
			item.Chunks = summary.Chunks
			item.Bytes = summary.Bytes
		}
		sources = append(sources, item)
	}
	writeJSON(w, map[string]any{"sources": sources, "collections": collections})
}

func isLoopbackHost(host string) bool {
	return host == "localhost" || host == "::1" || strings.HasPrefix(host, "127.")
}

type startKnowledgeIndexRequest struct {
	SourceID     string `json:"source_id"`
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementStartIndex(w http.ResponseWriter, r *http.Request) {
	var input startKnowledgeIndexRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.SourceID == "" || input.Confirmation != input.SourceID {
		writeAPIError(w, http.StatusBadRequest, "index request requires an exact source ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeKnowledgeJobs()) > 0 || len(s.activeModelPullJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusTooManyRequests, "another Brain runtime job is active", "api_error", "runtime_job_limit")
		return
	}
	config, err := s.loadKnowledgeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge configuration is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	source, ok := findKnowledgeSource(config, input.SourceID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "knowledge source is not registered in Brain", "invalid_request_error", "knowledge_source_not_found")
		return
	}
	root, err := s.resolveKnowledgeSource(source)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "registered knowledge root is unavailable", "invalid_request_error", "knowledge_root_unavailable")
		return
	}
	if s.cfg.StorePath == "" || s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge runtime is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	embedModel := source.EmbeddingModel
	if embedModel == "" {
		embedModel = config.Aliases["embed"]
	}
	if embedModel == "" {
		embedModel = "nomic-embed-text"
	}
	backendCtx, backendCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer backendCancel()
	_, err = s.localKnowledgeEmbedding(backendCtx, config, embedModel)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "knowledge operations require a configured local embedding model/backend", "invalid_request_error", "local_embedding_required")
		return
	}
	err = s.cfg.Adapter.Ping(backendCtx)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "local embedding backend is unreachable", "invalid_request_error", "backend_unreachable")
		return
	}
	s.pruneKnowledgeJobs()
	s.knowledgeJobsMu.Lock()
	if len(s.knowledgeJobs) >= 32 {
		s.knowledgeJobsMu.Unlock()
		writeAPIError(w, http.StatusTooManyRequests, "knowledge job history is full; retry after older jobs expire", "api_error", "knowledge_job_history_full")
		return
	}
	for _, job := range s.knowledgeJobs {
		if isActiveKnowledgeJob(job.status) {
			s.knowledgeJobsMu.Unlock()
			writeAPIError(w, http.StatusTooManyRequests, "another Brain runtime job is active", "api_error", "runtime_job_limit")
			return
		}
	}
	id, err := newManagementLoginID()
	if err != nil {
		s.knowledgeJobsMu.Unlock()
		writeAPIError(w, http.StatusInternalServerError, "knowledge index job could not be started", "api_error", "knowledge_job_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), knowledgeIndexTimeout)
	now := time.Now().UTC()
	job := &managementKnowledgeJob{
		id: id, sourceID: source.ID, name: source.Name, collection: source.Collection,
		root: root, embedModel: embedModel,
		status: "queued", createdAt: now, updatedAt: now,
		expiresAt: now.Add(knowledgeIndexTimeout), cancel: cancel,
	}
	s.knowledgeJobs[id] = job
	s.knowledgeJobsWG.Add(1)
	s.knowledgeJobsMu.Unlock()
	go func() {
		defer s.knowledgeJobsWG.Done()
		s.runManagementIndex(ctx, job, source)
	}()
	w.Header().Set("Location", "/manage/v1/knowledge/index-jobs/"+id)
	s.writeKnowledgeJob(w, http.StatusAccepted, job)
}

func (s *Server) runManagementIndex(ctx context.Context, job *managementKnowledgeJob, source home.KnowledgeSource) {
	defer job.cancel()
	config, err := s.loadKnowledgeConfig()
	if err != nil {
		s.finishKnowledgeJob(job, "failed", "knowledge_unavailable")
		return
	}
	current, ok := findKnowledgeSource(config, source.ID)
	if !ok || current.Root != source.Root || current.Collection != source.Collection || current.EmbeddingModel != source.EmbeddingModel {
		s.finishKnowledgeJob(job, "failed", "knowledge_source_changed")
		return
	}
	root, err := s.resolveKnowledgeSource(current)
	if err != nil {
		s.finishKnowledgeJob(job, "failed", "knowledge_root_unavailable")
		return
	}
	db, err := store.Open(s.cfg.StorePath)
	if err != nil {
		s.finishKnowledgeJob(job, "failed", "knowledge_store_unavailable")
		return
	}
	defer db.Close()
	if s.cfg.Residency == nil {
		s.finishKnowledgeJob(job, "failed", "residency_unavailable")
		return
	}
	requestedEmbedModel := current.EmbeddingModel
	if requestedEmbedModel == "" {
		requestedEmbedModel = config.Aliases["embed"]
	}
	if requestedEmbedModel == "" {
		requestedEmbedModel = "nomic-embed-text"
	}
	if requestedEmbedModel != job.embedModel {
		s.finishKnowledgeJob(job, "failed", "knowledge_source_changed")
		return
	}
	embedModel := requestedEmbedModel
	if collection, err := db.GetCollection(ctx, current.Collection); err == nil {
		embedModel = collection.Model
	} else if !errors.Is(err, store.ErrNotFound) {
		s.finishKnowledgeJob(job, "failed", "knowledge_store_unavailable")
		return
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 5*time.Second)
	embedBackendTag, err := s.localKnowledgeEmbedding(checkCtx, config, embedModel)
	checkCancel()
	if err != nil {
		s.finishKnowledgeJob(job, "failed", "local_embedding_required")
		return
	}
	s.updateKnowledgeJob(job, func(current *managementKnowledgeJob) { current.status = "running" })
	indexer := rag.NewIndexer(db, s.cfg.Adapter)
	indexer.AcquireEmbedding = func(embedCtx context.Context, model string) (func(), error) {
		if model != embedModel {
			return nil, errors.New("collection embedding model changed during indexing")
		}
		return s.cfg.Residency.Acquire(embedCtx, embedBackendTag, 0)
	}
	stats, err := indexer.IndexDirectory(ctx, root, current.Collection, embedModel, func(_ string, chunks int) {
		s.updateKnowledgeJob(job, func(current *managementKnowledgeJob) {
			current.filesIndexed++
			current.chunksCreated += chunks
		})
	})
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			s.finishKnowledgeJob(job, "cancelled", "")
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			s.finishKnowledgeJob(job, "failed", "knowledge_index_timeout")
		} else {
			s.finishKnowledgeJob(job, "failed", "knowledge_index_failed")
		}
		return
	}
	s.updateKnowledgeJob(job, func(current *managementKnowledgeJob) {
		current.status = "completed"
		current.filesScanned = stats.FilesScanned
		current.filesIndexed = stats.FilesIndexed
		current.filesSkipped = stats.FilesSkipped
		current.filesPruned = stats.FilesPruned
		current.chunksCreated = stats.ChunksCreated
		current.expiresAt = time.Now().UTC().Add(5 * time.Minute)
	})
}

func isActiveKnowledgeJob(status string) bool {
	return status == "queued" || status == "running" || status == "canceling"
}

func (s *Server) updateKnowledgeJob(job *managementKnowledgeJob, update func(*managementKnowledgeJob)) {
	s.knowledgeJobsMu.Lock()
	defer s.knowledgeJobsMu.Unlock()
	update(job)
	job.updatedAt = time.Now().UTC()
}

func (s *Server) finishKnowledgeJob(job *managementKnowledgeJob, status, errorCode string) {
	s.updateKnowledgeJob(job, func(current *managementKnowledgeJob) {
		current.status = status
		current.errorCode = errorCode
		current.expiresAt = time.Now().UTC().Add(5 * time.Minute)
	})
}

func (s *Server) pruneKnowledgeJobs() {
	now := time.Now()
	s.knowledgeJobsMu.Lock()
	defer s.knowledgeJobsMu.Unlock()
	for id, job := range s.knowledgeJobs {
		if !job.expiresAt.After(now) {
			if job.cancel != nil {
				job.cancel()
			}
			delete(s.knowledgeJobs, id)
		}
	}
}

func (s *Server) activeKnowledgeJobs() []managementKnowledgeJob {
	s.knowledgeJobsMu.Lock()
	defer s.knowledgeJobsMu.Unlock()
	var active []managementKnowledgeJob
	for _, job := range s.knowledgeJobs {
		if isActiveKnowledgeJob(job.status) {
			active = append(active, *job)
		}
	}
	return active
}

func managementKnowledgeJobResponse(job *managementKnowledgeJob) map[string]any {
	return map[string]any{
		"id": job.id, "source_id": job.sourceID, "source_name": job.name, "collection": job.collection,
		"status": job.status, "error_code": job.errorCode,
		"files_indexed": job.filesIndexed, "chunks_created": job.chunksCreated,
		"files_scanned": job.filesScanned, "files_skipped": job.filesSkipped, "files_pruned": job.filesPruned,
		"created_at": job.createdAt.Format(time.RFC3339), "updated_at": job.updatedAt.Format(time.RFC3339),
	}
}

func (s *Server) copyKnowledgeJob(id string) *managementKnowledgeJob {
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		return nil
	}
	s.pruneKnowledgeJobs()
	s.knowledgeJobsMu.Lock()
	defer s.knowledgeJobsMu.Unlock()
	job := s.knowledgeJobs[id]
	if job == nil {
		return nil
	}
	copy := *job
	return &copy
}

func (s *Server) handleManagementIndexJobs(w http.ResponseWriter, _ *http.Request) {
	s.pruneKnowledgeJobs()
	s.knowledgeJobsMu.Lock()
	jobs := make([]managementKnowledgeJob, 0, len(s.knowledgeJobs))
	for _, job := range s.knowledgeJobs {
		jobs = append(jobs, *job)
	}
	s.knowledgeJobsMu.Unlock()
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].createdAt.After(jobs[j].createdAt) })
	items := make([]map[string]any, 0, len(jobs))
	for i := range jobs {
		items = append(items, managementKnowledgeJobResponse(&jobs[i]))
	}
	writeJSON(w, map[string]any{"jobs": items})
}

func (s *Server) writeKnowledgeJob(w http.ResponseWriter, status int, job *managementKnowledgeJob) {
	s.knowledgeJobsMu.Lock()
	copy := *job
	s.knowledgeJobsMu.Unlock()
	writeJSONStatus(w, status, managementKnowledgeJobResponse(&copy))
}

func (s *Server) handleManagementIndexStatus(w http.ResponseWriter, r *http.Request) {
	job := s.copyKnowledgeJob(r.PathValue("id"))
	if job == nil {
		writeAPIError(w, http.StatusNotFound, "knowledge index job not found", "invalid_request_error", "not_found")
		return
	}
	writeJSON(w, managementKnowledgeJobResponse(job))
}

func (s *Server) handleManagementCancelIndex(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || strings.ContainsAny(id, "/\\\x00") {
		writeAPIError(w, http.StatusNotFound, "knowledge index job not found", "invalid_request_error", "not_found")
		return
	}
	s.knowledgeJobsMu.Lock()
	job := s.knowledgeJobs[id]
	if job == nil {
		s.knowledgeJobsMu.Unlock()
		writeAPIError(w, http.StatusNotFound, "knowledge index job not found", "invalid_request_error", "not_found")
		return
	}
	if isActiveKnowledgeJob(job.status) {
		job.status = "canceling"
		job.updatedAt = time.Now().UTC()
		if job.cancel != nil {
			job.cancel()
		}
	}
	copy := *job
	s.knowledgeJobsMu.Unlock()
	s.writeKnowledgeJob(w, http.StatusAccepted, &copy)
}

type knowledgeSearchRequest struct {
	SourceID string `json:"source_id"`
	Query    string `json:"query"`
	Limit    int    `json:"limit,omitempty"`
}

type knowledgeCitation struct {
	Path      string  `json:"path"`
	StartLine int     `json:"start_line"`
	EndLine   int     `json:"end_line"`
	Score     float64 `json:"score"`
	MatchType string  `json:"match_type"`
	Snippet   string  `json:"snippet"`
}

func (s *Server) handleManagementKnowledgeSearch(w http.ResponseWriter, r *http.Request) {
	var input knowledgeSearchRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.SourceID == "" || strings.TrimSpace(input.Query) == "" || utf8.RuneCountInString(input.Query) > maxKnowledgeQueryRunes || input.Limit < 0 {
		writeAPIError(w, http.StatusBadRequest, "search requires a source ID and a query of at most 4096 characters", "invalid_request_error", "bad_request")
		return
	}
	if input.Limit == 0 {
		input.Limit = 5
	}
	if input.Limit > maxKnowledgeSearchLimit {
		input.Limit = maxKnowledgeSearchLimit
	}
	s.runtimeMutationMu.Lock()
	defer s.runtimeMutationMu.Unlock()
	if len(s.activeModelPullJobs()) > 0 || len(s.activeManagementEvalJobs()) > 0 {
		writeAPIError(w, http.StatusConflict, "wait for active model/evaluation work before searching knowledge", "invalid_request_error", "runtime_job_in_progress")
		return
	}
	config, err := s.loadKnowledgeConfig()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge configuration is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	source, ok := findKnowledgeSource(config, input.SourceID)
	if !ok {
		writeAPIError(w, http.StatusNotFound, "knowledge source is not registered in Brain", "invalid_request_error", "knowledge_source_not_found")
		return
	}
	if _, err := s.resolveKnowledgeSource(source); err != nil {
		writeAPIError(w, http.StatusConflict, "registered knowledge root is unavailable", "invalid_request_error", "knowledge_root_unavailable")
		return
	}
	embedModel := source.EmbeddingModel
	if embedModel == "" {
		embedModel = config.Aliases["embed"]
	}
	if embedModel == "" {
		embedModel = "nomic-embed-text"
	}
	backendCtx, backendCancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer backendCancel()
	if _, err := s.localKnowledgeEmbedding(backendCtx, config, embedModel); err != nil {
		writeAPIError(w, http.StatusConflict, "knowledge search requires a configured local embedding model/backend", "invalid_request_error", "local_embedding_required")
		return
	}
	if s.cfg.StorePath == "" || s.cfg.Adapter == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge store is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	db, err := store.Open(s.cfg.StorePath)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain knowledge store is unavailable", "api_error", "knowledge_unavailable")
		return
	}
	defer db.Close()
	collection, err := db.GetCollection(ctx, source.Collection)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "knowledge collection is not indexed", "invalid_request_error", "knowledge_collection_not_found")
		return
	}
	embedBackendTag, err := s.localKnowledgeEmbedding(ctx, config, collection.Model)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "collection uses an unavailable or non-local embedding model", "invalid_request_error", "local_embedding_required")
		return
	}
	if s.cfg.Residency == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain model residency state is unavailable", "api_error", "residency_unavailable")
		return
	}
	release, err := s.cfg.Residency.Acquire(ctx, embedBackendTag, 0)
	if err != nil {
		writeAPIError(w, http.StatusTooManyRequests, "local embedding model is currently unavailable", "api_error", "residency_unavailable")
		return
	}
	defer release()
	searcher := rag.NewSearcher(db, s.cfg.Adapter)
	results, err := searcher.Search(ctx, source.Collection, strings.TrimSpace(input.Query), input.Limit)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "local knowledge search failed", "api_error", "knowledge_search_failed")
		return
	}
	citations := make([]knowledgeCitation, 0, len(results))
	for _, result := range results {
		rel := filepath.Clean(result.Chunk.Path)
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		snippet := []rune(result.Chunk.Content)
		if len(snippet) > maxKnowledgeSnippetRunes {
			snippet = snippet[:maxKnowledgeSnippetRunes]
		}
		citations = append(citations, knowledgeCitation{
			Path: filepath.ToSlash(rel), StartLine: result.Chunk.StartLine, EndLine: result.Chunk.EndLine,
			Score: result.Score, MatchType: result.MatchType, Snippet: string(snippet),
		})
	}
	writeJSON(w, map[string]any{"source_id": source.ID, "collection": source.Collection, "results": citations})
}

func (s *Server) localKnowledgeEmbedding(ctx context.Context, config *home.Config, modelID string) (string, error) {
	if s.cfg.Adapter == nil || s.cfg.Adapter.Name() != "ollama" {
		return "", errors.New("knowledge management requires the local Ollama adapter")
	}
	backendID, backendConfig := config.PrimaryBackend()
	if backendID != s.runningBackendID() {
		return "", errors.New("configured backend differs from the running adapter")
	}
	endpoint, err := url.Parse(backendConfig.Endpoint)
	if err != nil || !isLoopbackHost(endpoint.Hostname()) || (endpoint.Scheme != "http" && endpoint.Scheme != "https") {
		return "", errors.New("knowledge embeddings require a loopback backend")
	}
	model, ok := s.cfg.Catalog.GetModel(modelID)
	if !ok || model.Backends["ollama"] == "" {
		return "", errors.New("embedding model is not in the local catalog")
	}
	embeds := false
	for _, role := range model.Roles {
		if role == catalog.RoleEmbed {
			embeds = true
			break
		}
	}
	if !embeds {
		return "", errors.New("configured model is not catalogued as an embedding model")
	}
	installed, err := s.cfg.Adapter.ListModels(ctx)
	if err != nil {
		return "", errors.New("local model inventory is unavailable")
	}
	tag := model.Backends["ollama"]
	found := false
	for _, item := range installed {
		if item.Name == tag || item.Name == model.ID {
			found = true
			break
		}
	}
	if !found {
		return "", errors.New("embedding model must be installed before indexing/search")
	}
	return tag, nil
}
