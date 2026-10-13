package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/skills"
	"gopkg.in/yaml.v3"
)

// HardenResult reports the outcome of the bounded refinement rounds.
type HardenResult struct {
	SkillName        string  `json:"skill_name"`
	Rounds           int     `json:"rounds"`
	InitialPrecision float64 `json:"initial_precision"`
	FinalPrecision   float64 `json:"final_precision"`
	InitialRecall    float64 `json:"initial_recall"`
	FinalRecall      float64 `json:"final_recall"`
	Improved         bool    `json:"improved"`
}

// Hardener performs bounded optimization of skill descriptions and procedures.
type Hardener struct {
	CandidatesDir string
	Evaluator     *Evaluator
	MaxRounds     int
}

// NewHardener creates a candidate Hardener.
func NewHardener(candidatesDir string, evaluator *Evaluator, maxRounds int) *Hardener {
	if maxRounds <= 0 {
		maxRounds = 3
	}
	return &Hardener{
		CandidatesDir: filepath.Clean(candidatesDir),
		Evaluator:     evaluator,
		MaxRounds:     maxRounds,
	}
}

// Harden iterates over description and workflow prompt refinement.
func (h *Hardener) Harden(ctx context.Context, skillName string) (*HardenResult, error) {
	candidateDir := filepath.Join(h.CandidatesDir, skillName)
	skillPath := filepath.Join(candidateDir, "SKILL.md")

	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("read candidate skill at %s: %w", skillPath, err)
	}

	initialRep, err := h.Evaluator.EvaluateCandidate(ctx, skillName)
	if err != nil {
		return nil, fmt.Errorf("initial candidate evaluation: %w", err)
	}

	res := &HardenResult{
		SkillName:        skillName,
		InitialPrecision: initialRep.Precision,
		FinalPrecision:   initialRep.Precision,
		InitialRecall:    initialRep.Recall,
		FinalRecall:      initialRep.Recall,
	}

	fm, body, err := skills.ParseSkillMD(data)
	if err != nil {
		return nil, fmt.Errorf("parse candidate skill: %w", err)
	}

	currentDesc := fm.Description
	currentBody := body

	for round := 1; round <= h.MaxRounds; round++ {
		res.Rounds = round

		// If precision or recall is below 1.0, refine description keywords
		keywords := strings.ReplaceAll(fm.Name, "-", " ")
		refinedDesc := fmt.Sprintf("Use when %s. Automates %s workflows with validated verification.", keywords, keywords)
		if refinedDesc == currentDesc {
			refinedDesc = fmt.Sprintf("Use when performing %s. Follows specific procedure for %s.", keywords, keywords)
		}

		newFm := skills.Frontmatter{
			Name:        fm.Name,
			Description: refinedDesc,
		}
		newFmBytes, err := yaml.Marshal(newFm)
		if err != nil {
			continue
		}
		newContent := fmt.Sprintf("---\n%s---\n\n%s", string(newFmBytes), currentBody)

		_ = os.WriteFile(skillPath, []byte(newContent), 0o600)

		rep, err := h.Evaluator.EvaluateCandidate(ctx, skillName)
		if err == nil {
			if rep.Precision >= res.FinalPrecision && rep.Recall >= res.FinalRecall {
				res.FinalPrecision = rep.Precision
				res.FinalRecall = rep.Recall
				res.Improved = true
				currentDesc = refinedDesc
			} else {
				// Revert if no improvement
				revertFm := skills.Frontmatter{
					Name:        fm.Name,
					Description: currentDesc,
				}
				revertBytes, _ := yaml.Marshal(revertFm)
				revertContent := fmt.Sprintf("---\n%s---\n\n%s", string(revertBytes), currentBody)
				_ = os.WriteFile(skillPath, []byte(revertContent), 0o600)
			}
		}

		if res.FinalPrecision >= 1.0 && res.FinalRecall >= 1.0 {
			break
		}
	}

	return res, nil
}
