package gateway

import (
	"net/http"
	"os"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/forge"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

type managementForgeCandidate struct {
	ID                      string               `json:"id"`
	State                   string               `json:"state"`
	Reviewed                bool                 `json:"reviewed"`
	ExternalContentReviewed bool                 `json:"external_content_reviewed"`
	ExternalContentIncluded bool                 `json:"external_content_included,omitempty"`
	SourceSessionCount      int                  `json:"source_session_count"`
	StructuralPassed        bool                 `json:"structural_passed"`
	StructuralFields        []string             `json:"structural_fields,omitempty"`
	SafetyPassed            bool                 `json:"safety_passed"`
	SafetyViolations        []string             `json:"safety_violations,omitempty"`
	EvaluationPassed        bool                 `json:"evaluation_passed"`
	EvaluationFresh         bool                 `json:"evaluation_fresh"`
	PromotionReady          bool                 `json:"promotion_ready"`
	Evaluation              *managementForgeEval `json:"evaluation,omitempty"`
}

type managementForgeEval struct {
	EvaluatedAt     time.Time                  `json:"evaluated_at"`
	Passed          bool                       `json:"passed"`
	Precision       float64                    `json:"precision"`
	Recall          float64                    `json:"recall"`
	TokenSavingsPct float64                    `json:"token_savings_pct"`
	Benchmarks      []managementForgeBenchmark `json:"benchmarks"`
}

type managementForgeBenchmark struct {
	FixtureName string `json:"fixture_name"`
	FixtureType string `json:"fixture_type"`
	Passed      bool   `json:"passed"`
}

func (s *Server) forgeCandidatesRoot() (string, error) {
	layout, err := home.Default()
	if err != nil {
		return "", err
	}
	return canonicalBrainHomeChild(layout.Root, layout.Candidates)
}

func (s *Server) forgeCandidateDTO(root, name string) managementForgeCandidate {
	id := forge.CandidateAlias(s.cfg.Token, name)
	check, err := forge.InspectCandidate(root, name)
	if err != nil {
		return managementForgeCandidate{ID: id, State: "invalid_candidate"}
	}
	item := managementForgeCandidate{
		ID: id, Reviewed: check.Reviewed, ExternalContentReviewed: check.Reviewed,
		StructuralPassed: check.StructuralPassed, StructuralFields: check.StructuralFields,
		SafetyPassed: check.SafetyPassed, SafetyViolations: check.SafetyViolations,
		EvaluationPassed: check.EvaluationPassed, EvaluationFresh: check.EvaluationFresh,
		PromotionReady: check.PromotionReady && !check.ExternalContent,
	}
	if check.Provenance != nil {
		item.SourceSessionCount = len(check.Provenance.SourceSessions)
		if check.Reviewed {
			item.ExternalContentIncluded = check.ExternalContent
		}
	}
	if report := check.Evaluation; report != nil {
		benchmarks := make([]managementForgeBenchmark, 0, len(report.Benchmarks))
		for _, benchmark := range report.Benchmarks {
			benchmarks = append(benchmarks, managementForgeBenchmark{FixtureName: benchmark.FixtureName, FixtureType: benchmark.FixtureType, Passed: benchmark.Passed})
		}
		item.Evaluation = &managementForgeEval{
			EvaluatedAt: report.EvaluatedAt, Passed: report.Passed, Precision: report.Precision,
			Recall: report.Recall, TokenSavingsPct: report.TokenSavingsPct, Benchmarks: benchmarks,
		}
	}
	switch {
	case !item.StructuralPassed:
		item.State = "validation_failed"
	case !item.SafetyPassed:
		item.State = "safety_blocked"
	case !item.Reviewed:
		item.State = "review_required"
	case item.ExternalContentIncluded:
		item.State = "external_content_cli_only"
	case !item.EvaluationPassed:
		item.State = "evaluation_required"
	default:
		item.State = "ready_to_promote"
	}
	return item
}

func (s *Server) resolveForgeCandidate(alias string) (string, string, error) {
	root, err := s.forgeCandidatesRoot()
	if err != nil {
		return "", "", err
	}
	name, err := forge.CandidateNameForAlias(root, s.cfg.Token, alias)
	if err != nil {
		return "", "", err
	}
	return root, name, nil
}

func (s *Server) handleManagementForgeCandidates(w http.ResponseWriter, _ *http.Request) {
	root, err := s.forgeCandidatesRoot()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain Forge candidate storage is unavailable", "api_error", "forge_unavailable")
		return
	}
	names, err := forge.CandidateNames(root)
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain Forge candidate inventory is unavailable", "api_error", "forge_unavailable")
		return
	}
	items := make([]managementForgeCandidate, 0, len(names))
	for _, name := range names {
		items = append(items, s.forgeCandidateDTO(root, name))
	}
	writeJSON(w, map[string]any{"candidates": items})
}

type forgeCandidateActionRequest struct {
	Confirmation string `json:"confirmation"`
}

func (s *Server) handleManagementValidateCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input forgeCandidateActionRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "validate "+id {
		writeAPIError(w, http.StatusBadRequest, "validation requires the exact candidate ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.forgeMutationMu.Lock()
	defer s.forgeMutationMu.Unlock()
	root, name, err := s.resolveForgeCandidate(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Forge candidate not found", "invalid_request_error", "candidate_not_found")
		return
	}
	writeJSON(w, s.forgeCandidateDTO(root, name))
}

func (s *Server) handleManagementEvaluateCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input forgeCandidateActionRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "evaluate "+id {
		writeAPIError(w, http.StatusBadRequest, "evaluation requires the exact candidate ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.forgeMutationMu.Lock()
	defer s.forgeMutationMu.Unlock()
	root, name, err := s.resolveForgeCandidate(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Forge candidate not found", "invalid_request_error", "candidate_not_found")
		return
	}
	report, err := forge.NewEvaluator(root, nil).EvaluateCandidate(r.Context(), name)
	if err != nil || report == nil {
		writeAPIError(w, http.StatusConflict, "Forge candidate evaluation could not be completed", "invalid_request_error", "candidate_evaluation_failed")
		return
	}
	writeJSON(w, s.forgeCandidateDTO(root, name))
}

func (s *Server) handleManagementPromoteCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input forgeCandidateActionRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "promote "+id {
		writeAPIError(w, http.StatusBadRequest, "promotion requires the exact candidate ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.forgeMutationMu.Lock()
	defer s.forgeMutationMu.Unlock()
	root, name, err := s.resolveForgeCandidate(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Forge candidate not found", "invalid_request_error", "candidate_not_found")
		return
	}
	check, err := forge.InspectCandidate(root, name)
	if err != nil || !check.PromotionReady {
		writeAPIError(w, http.StatusConflict, "candidate must pass validation, secret scan, owner review, and fresh evaluation before promotion", "invalid_request_error", "promotion_gates_failed")
		return
	}
	if check.ExternalContent {
		writeAPIError(w, http.StatusConflict, "candidate with external content must be promoted locally after project-scoped review", "invalid_request_error", "external_candidate_cli_only")
		return
	}
	layout, err := home.Default()
	if err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "Brain skills home is unavailable", "api_error", "skills_unavailable")
		return
	}
	skillsRoot, err := canonicalBrainHomeChild(layout.Root, layout.Skills)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "Brain-user skill destination is not a safe private-home child", "invalid_request_error", "skills_destination_unsafe")
		return
	}
	result, err := forge.NewPromoter(root, skillsRoot).Promote(name)
	if err != nil || result == nil {
		writeAPIError(w, http.StatusConflict, "candidate promotion failed; recheck review/evaluation state", "invalid_request_error", "promotion_failed")
		return
	}
	writeJSON(w, map[string]any{
		"candidate_id": id, "promoted": true, "content_hash": result.ContentHash,
		"promoted_at": result.PromotedAt, "backup_created": result.BackupPath != "",
	})
}

func (s *Server) handleManagementDiscardCandidate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input forgeCandidateActionRequest
	if err := decodeManagementJSON(w, r, &input); err != nil || input.Confirmation != "discard "+id {
		writeAPIError(w, http.StatusBadRequest, "discard requires the exact candidate ID confirmation", "invalid_request_error", "bad_request")
		return
	}
	s.forgeMutationMu.Lock()
	defer s.forgeMutationMu.Unlock()
	root, name, err := s.resolveForgeCandidate(id)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "Forge candidate not found", "invalid_request_error", "candidate_not_found")
		return
	}
	dir, err := forge.CandidateDirectory(root, name)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "Forge candidate could not be removed safely", "invalid_request_error", "candidate_remove_failed")
		return
	}
	if err := os.RemoveAll(dir); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "Forge candidate could not be discarded", "api_error", "candidate_remove_failed")
		return
	}
	writeJSON(w, map[string]any{"candidate_id": id, "discarded": true})
}
