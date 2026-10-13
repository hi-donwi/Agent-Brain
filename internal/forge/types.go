package forge

import (
	"time"
)

// Provenance tracks the origin and training sources of a candidate skill.
type Provenance struct {
	ClusterID      string    `yaml:"cluster_id" json:"cluster_id"`
	CreatedAt      time.Time `yaml:"created_at" json:"created_at"`
	SourceSessions []string  `yaml:"source_sessions" json:"source_sessions"`
	SourceModels   []string  `yaml:"source_models" json:"source_models"`
	ToolSequence   []string  `yaml:"tool_sequence,omitempty" json:"tool_sequence,omitempty"`
	SafetyChecked  bool      `yaml:"safety_checked" json:"safety_checked"`
	SafetyFlags    []string  `yaml:"safety_flags,omitempty" json:"safety_flags,omitempty"`
}

// TraceCluster groups repeated successful sessions by tool signatures or intent.
type TraceCluster struct {
	ID              string   `json:"id"`
	Signature       string   `json:"signature"`
	Intent          string   `json:"intent"`
	SuggestedName   string   `json:"suggested_name"`
	SessionIDs      []string `json:"session_ids"`
	TurnCount       int      `json:"turn_count"`
	SuccessfulRuns  int      `json:"successful_runs"`
	AvgPromptTokens int      `json:"avg_prompt_tokens"`
	AvgEvalTokens   int      `json:"avg_eval_tokens"`
}

// CandidateSkill represents a draft skill proposed by the forge.
type CandidateSkill struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Body        string     `json:"body"`
	Path        string     `json:"path"`
	Provenance  Provenance `json:"provenance"`
}

// BenchmarkResult records evaluation metrics for a candidate skill against baseline.
type BenchmarkResult struct {
	SkillName       string `json:"skill_name"`
	FixtureName     string `json:"fixture_name"`
	FixtureType     string `json:"fixture_type"` // direct, paraphrase, near_miss
	Triggered       bool   `json:"triggered"`
	ExpectedTrigger bool   `json:"expected_trigger"`
	Passed          bool   `json:"passed"`
	TokensBaseline  int    `json:"tokens_baseline"`
	TokensWithSkill int    `json:"tokens_with_skill"`
	DurationMs      int64  `json:"duration_ms"`
	Notes           string `json:"notes,omitempty"`
}

// CandidateEvalReport summarizes the evaluation and verification of a candidate skill.
type CandidateEvalReport struct {
	SkillName        string            `json:"skill_name"`
	EvaluatedAt      time.Time         `json:"evaluated_at"`
	Passed           bool              `json:"passed"`
	Precision        float64           `json:"precision"`
	Recall           float64           `json:"recall"`
	TokenSavingsPct  float64           `json:"token_savings_pct"`
	Benchmarks       []BenchmarkResult `json:"benchmarks"`
	SafetyPassed     bool              `json:"safety_passed"`
	SafetyViolations []string          `json:"safety_violations,omitempty"`
}
