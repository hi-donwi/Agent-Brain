package forge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/skills"
	"github.com/hi-donwi/Agent-Local/internal/trace"
	"gopkg.in/yaml.v3"
)

// Drafter generates candidate SKILL.md documents from trace clusters.
type Drafter struct {
	CandidatesDir string
	Store         *trace.Store
	Adapter       backend.Adapter
}

// NewDrafter initializes a Drafter rooted at candidatesDir.
func NewDrafter(candidatesDir string, store *trace.Store, adapter backend.Adapter) *Drafter {
	return &Drafter{
		CandidatesDir: filepath.Clean(candidatesDir),
		Store:         store,
		Adapter:       adapter,
	}
}

// Draft synthesizes a CandidateSkill from a cluster and saves it to candidatesDir.
func (d *Drafter) Draft(ctx context.Context, cluster TraceCluster, overrideName string) (*CandidateSkill, error) {
	name := slugify(overrideName)
	if name == "" {
		name = cluster.SuggestedName
	}
	if name == "" {
		name = "candidate-" + cluster.ID
	}

	// 1. Collect trace turns across cluster sessions for synthesis
	var samplePrompts []string
	var sampleResponses []string
	var modelsUsed []string
	modelSeen := make(map[string]bool)

	if d.Store != nil {
		for _, sessID := range cluster.SessionIDs {
			recs, err := d.Store.ReadSession(sessID)
			if err != nil {
				continue
			}
			for _, r := range recs {
				if r.Model != "" && !modelSeen[r.Model] {
					modelSeen[r.Model] = true
					modelsUsed = append(modelsUsed, r.Model)
				}
				for _, m := range r.Messages {
					if m.Role == "user" && len(samplePrompts) < 5 {
						samplePrompts = append(samplePrompts, m.Content)
					}
				}
				if r.Response != "" && len(sampleResponses) < 3 {
					sampleResponses = append(sampleResponses, r.Response)
				}
			}
		}
	}

	// 2. Synthesize Description and Body
	description := fmt.Sprintf("Use when %s. Guides execution of %s workflows.", cluster.Intent, name)
	if cluster.Signature != "" {
		description = fmt.Sprintf("Use when %s. Automates tool sequence: %s.", cluster.Intent, cluster.Signature)
	}

	var bodyBuilder strings.Builder
	bodyBuilder.WriteString(fmt.Sprintf("# %s\n\n", strings.Title(strings.ReplaceAll(name, "-", " "))))
	bodyBuilder.WriteString(fmt.Sprintf("## Purpose\n\nAutomates and standardizes the workflow for: %s.\n\n", cluster.Intent))

	if cluster.Signature != "" {
		bodyBuilder.WriteString("## Recommended Tool Sequence\n\n")
		tools := strings.Split(cluster.Signature, " -> ")
		for i, t := range tools {
			bodyBuilder.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, strings.TrimSpace(t)))
		}
		bodyBuilder.WriteString("\n")
	}

	bodyBuilder.WriteString("## Workflow Rules\n\n")
	bodyBuilder.WriteString("1. Verify input context before initiating actions.\n")
	bodyBuilder.WriteString("2. Execute steps sequentially and check outputs for errors.\n")
	bodyBuilder.WriteString("3. Report outcome concisely with relevant evidence.\n\n")

	if len(samplePrompts) > 0 {
		bodyBuilder.WriteString("## Example Triggers\n\n")
		for _, p := range samplePrompts {
			cleanP := strings.TrimSpace(p)
			if len(cleanP) > 120 {
				cleanP = cleanP[:120] + "..."
			}
			bodyBuilder.WriteString(fmt.Sprintf("- \"%s\"\n", cleanP))
		}
		bodyBuilder.WriteString("\n")
	}

	fm := skills.Frontmatter{
		Name:        name,
		Description: description,
	}
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("marshal frontmatter: %w", err)
	}

	skillMDContent := fmt.Sprintf("---\n%s---\n\n%s", string(fmBytes), bodyBuilder.String())

	// 3. Prepare candidate folder and write files
	candidateDir := filepath.Join(d.CandidatesDir, name)
	if err := os.MkdirAll(candidateDir, 0o700); err != nil {
		return nil, fmt.Errorf("create candidate dir %s: %w", candidateDir, err)
	}

	skillFilePath := filepath.Join(candidateDir, "SKILL.md")
	if err := os.WriteFile(skillFilePath, []byte(skillMDContent), 0o600); err != nil {
		return nil, fmt.Errorf("write candidate SKILL.md: %w", err)
	}

	var toolSeq []string
	if cluster.Signature != "" {
		for _, t := range strings.Split(cluster.Signature, " -> ") {
			toolSeq = append(toolSeq, strings.TrimSpace(t))
		}
	}

	prov := Provenance{
		ClusterID:      cluster.ID,
		CreatedAt:      time.Now().UTC(),
		SourceSessions: cluster.SessionIDs,
		SourceModels:   modelsUsed,
		ToolSequence:   toolSeq,
		SafetyChecked:  false,
	}

	provBytes, err := yaml.Marshal(prov)
	if err == nil {
		_ = os.WriteFile(filepath.Join(candidateDir, "provenance.yaml"), provBytes, 0o600)
	}

	return &CandidateSkill{
		Name:        name,
		Description: description,
		Body:        bodyBuilder.String(),
		Path:        candidateDir,
		Provenance:  prov,
	}, nil
}
