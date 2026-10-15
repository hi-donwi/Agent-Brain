package forge

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/skills"
	"gopkg.in/yaml.v3"
)

var (
	// Secret and credential patterns
	privateKeyRegex = regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	awsKeyRegex     = regexp.MustCompile(`AKIA[0-9A-Z]{16}`)
	githubTokenRe   = regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{36,}`)
	openaiTokenRe   = regexp.MustCompile(`sk-[a-zA-Z0-9]{32,}`)
	slackTokenRe    = regexp.MustCompile(`xox[baprs]-[0-9a-zA-Z]{10,}`)
	passwordRe      = regexp.MustCompile(`(?i)password\s*[:=]\s*["'][^"']+["']`)

	// Prompt injection detection
	injectionRegex = regexp.MustCompile(`(?i)(ignore (all )?previous instructions|disregard prior directives|system override)`)
)

// ScanSafety checks skill text against secret patterns and prompt injection.
func ScanSafety(data []byte) []string {
	content := string(data)
	var violations []string

	if privateKeyRegex.MatchString(content) {
		violations = append(violations, "planted or leaked private key block detected")
	}
	if awsKeyRegex.MatchString(content) {
		violations = append(violations, "AWS access key pattern detected")
	}
	if githubTokenRe.MatchString(content) {
		violations = append(violations, "GitHub token pattern detected")
	}
	if openaiTokenRe.MatchString(content) {
		violations = append(violations, "OpenAI API key pattern detected")
	}
	if slackTokenRe.MatchString(content) {
		violations = append(violations, "Slack token pattern detected")
	}
	if passwordRe.MatchString(content) {
		violations = append(violations, "hardcoded password assignment detected")
	}
	if injectionRegex.MatchString(content) {
		violations = append(violations, "potential prompt injection or instruction override detected")
	}

	return violations
}

// Promoter handles promoting, rolling back, and exporting skills.
type Promoter struct {
	CandidatesDir string
	SkillsDir     string
}

// NewPromoter creates a Promoter.
func NewPromoter(candidatesDir, skillsDir string) *Promoter {
	return &Promoter{
		CandidatesDir: filepath.Clean(candidatesDir),
		SkillsDir:     filepath.Clean(skillsDir),
	}
}

// PromotionResult records the result of promoting a candidate skill.
type PromotionResult struct {
	SkillName   string    `json:"skill_name"`
	TargetDir   string    `json:"target_dir"`
	ContentHash string    `json:"content_hash"`
	PromotedAt  time.Time `json:"promoted_at"`
	BackupPath  string    `json:"backup_path,omitempty"`
}

// Promote promotes a candidate skill into the active skills directory.
func (p *Promoter) Promote(candidateName string, force bool) (*PromotionResult, error) {
	candidateDir := filepath.Join(p.CandidatesDir, candidateName)
	skillPath := filepath.Join(candidateDir, "SKILL.md")

	data, err := os.ReadFile(skillPath)
	if err != nil {
		return nil, fmt.Errorf("read candidate skill %s: %w", skillPath, err)
	}

	// 1. Structural validation
	valResult := skills.ValidateDir(candidateDir)
	if !valResult.Valid {
		return nil, fmt.Errorf("skill validation failed: %v", valResult.Errors)
	}

	// 2. Safety scan
	violations := ScanSafety(data)
	if len(violations) > 0 {
		return nil, fmt.Errorf("safety check failed with violations: %s", strings.Join(violations, ", "))
	}

	// 3. Eval check unless forced
	evalReportPath := filepath.Join(candidateDir, "eval_report.json")
	if !force {
		evalData, err := os.ReadFile(evalReportPath)
		if err != nil {
			return nil, errors.New("candidate has not been evaluated yet; run 'lokal forge eval' first or use --force")
		}
		var rep CandidateEvalReport
		if err := yaml.Unmarshal(evalData, &rep); err == nil && !rep.Passed {
			return nil, errors.New("candidate evaluation report indicates failures; resolve before promoting")
		}
	}

	// 4. Compute content hash
	hashBytes := sha256.Sum256(data)
	contentHash := hex.EncodeToString(hashBytes[:])[:12]

	targetDir := filepath.Join(p.SkillsDir, candidateName)
	var backupPath string

	// 5. Backup existing version if present
	if _, err := os.Stat(targetDir); err == nil {
		historyDir := filepath.Join(p.SkillsDir, ".history")
		_ = os.MkdirAll(historyDir, 0o700)
		backupPath = filepath.Join(historyDir, fmt.Sprintf("%s.%s.bak", candidateName, contentHash))
		_ = copyDir(targetDir, backupPath)
	}

	// 6. Copy candidate to target
	if err := os.MkdirAll(targetDir, 0o700); err != nil {
		return nil, fmt.Errorf("create target skill dir %s: %w", targetDir, err)
	}

	if err := copyDir(candidateDir, targetDir); err != nil {
		return nil, fmt.Errorf("copy skill to %s: %w", targetDir, err)
	}

	return &PromotionResult{
		SkillName:   candidateName,
		TargetDir:   targetDir,
		ContentHash: contentHash,
		PromotedAt:  time.Now().UTC(),
		BackupPath:  backupPath,
	}, nil
}

// Rollback restores the most recent backup of a skill.
func (p *Promoter) Rollback(skillName string) (string, error) {
	historyDir := filepath.Join(p.SkillsDir, ".history")
	entries, err := os.ReadDir(historyDir)
	if err != nil {
		return "", fmt.Errorf("read skill history %s: %w", historyDir, err)
	}

	prefix := skillName + "."
	var matching []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".bak") {
			matching = append(matching, filepath.Join(historyDir, e.Name()))
		}
	}

	if len(matching) == 0 {
		return "", fmt.Errorf("no rollback backup found for skill %s", skillName)
	}

	sort.Strings(matching)
	latestBackup := matching[len(matching)-1]

	targetDir := filepath.Join(p.SkillsDir, skillName)
	_ = os.RemoveAll(targetDir)
	if err := copyDir(latestBackup, targetDir); err != nil {
		return "", fmt.Errorf("restore backup %s: %w", latestBackup, err)
	}

	return latestBackup, nil
}

// Export packages a skill into a standalone directory formatted for Agent-Skills repository.
func (p *Promoter) Export(skillName, outDir string) (string, error) {
	sourceDir := filepath.Join(p.SkillsDir, skillName)
	if _, err := os.Stat(sourceDir); err != nil {
		sourceDir = filepath.Join(p.CandidatesDir, skillName)
		if _, err := os.Stat(sourceDir); err != nil {
			return "", fmt.Errorf("skill %s not found in skills or candidates", skillName)
		}
	}

	destDir := filepath.Join(outDir, skillName)
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return "", fmt.Errorf("create export dir %s: %w", destDir, err)
	}

	if err := copyDir(sourceDir, destDir); err != nil {
		return "", fmt.Errorf("export skill: %w", err)
	}

	// Clean out internal eval reports or temporary files from export
	_ = os.Remove(filepath.Join(destDir, "eval_report.json"))

	return destDir, nil
}

func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
