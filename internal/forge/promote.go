package forge

import (
	"encoding/json"
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
func (p *Promoter) Promote(candidateName string) (*PromotionResult, error) {
	candidateDir, err := CandidateDirectory(p.CandidatesDir, candidateName)
	if err != nil {
		return nil, err
	}
	// 1. Structural validation
	valResult := skills.ValidateDir(candidateDir)
	if !valResult.Valid {
		return nil, fmt.Errorf("skill validation failed: %v", valResult.Errors)
	}

	// 2. Safety scan
	candidateHash, violations, err := ScanCandidateFiles(candidateDir)
	if err != nil {
		return nil, err
	}
	if len(violations) > 0 {
		return nil, fmt.Errorf("safety check failed with violations: %s", strings.Join(violations, ", "))
	}

	// 3. Require provenance review and a current evaluation report.
	provenanceData, err := skills.ReadSafeSkillFile(candidateDir, "provenance.yaml", maxCandidateFileBytes)
	if err != nil {
		return nil, ErrCandidateNotReviewed
	}
	var provenance Provenance
	if err := yaml.Unmarshal(provenanceData, &provenance); err != nil || !provenance.SafetyChecked || provenance.ReviewedAt == nil || provenance.ReviewedContentHash != candidateHash || len(provenance.SourceSessions) == 0 {
		return nil, ErrCandidateNotReviewed
	}
	evalData, err := skills.ReadSafeSkillFile(candidateDir, "eval_report.json", maxCandidateFileBytes)
	if err != nil {
		return nil, ErrCandidateNotEvaluated
	}
	var rep CandidateEvalReport
	if err := json.Unmarshal(evalData, &rep); err != nil || !rep.Passed || !rep.SafetyPassed || rep.ContentHash != candidateHash || !hasRequiredFixtures(rep.Benchmarks) {
		return nil, ErrCandidateNotEvaluated
	}

	// 4. Compute content hash
	contentHash := candidateHash[:12]

	skillsRoot, err := ensureSkillsDirectory(p.SkillsDir)
	if err != nil {
		return nil, err
	}
	targetDir := filepath.Join(skillsRoot, candidateName)
	var backupPath string

	// 5. Backup existing version if present
	if targetInfo, err := os.Lstat(targetDir); err == nil {
		if targetInfo.Mode()&os.ModeSymlink != 0 || !targetInfo.IsDir() {
			return nil, ErrInvalidCandidate
		}
		historyDir := filepath.Join(skillsRoot, ".history")
		if err := ensureSafeDirectory(skillsRoot, historyDir); err != nil {
			return nil, err
		}
		backupName := fmt.Sprintf("%s.%s.bak", candidateName, contentHash)
		backupPath = filepath.Join(historyDir, backupName)
		if _, err := os.Lstat(backupPath); err == nil {
			backupPath = filepath.Join(historyDir, fmt.Sprintf("%s.%s.%d.bak", candidateName, contentHash, time.Now().UTC().UnixNano()))
		}
		if err := copyDir(targetDir, backupPath); err != nil {
			return nil, fmt.Errorf("backup existing Brain skill: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}

	// 6. Copy candidate to target
	if err := os.RemoveAll(targetDir); err != nil {
		return nil, fmt.Errorf("replace target Brain skill: %w", err)
	}

	if err := copyDir(candidateDir, targetDir); err != nil {
		if backupPath != "" {
			_ = copyDir(backupPath, targetDir)
		}
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

func ensureSkillsDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(real)
	if err != nil || !info.IsDir() {
		return "", ErrInvalidCandidate
	}
	return filepath.Clean(real), nil
}

func ensureSafeDirectory(root, path string) error {
	if !candidatePathWithin(root, path) {
		return ErrInvalidCandidate
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return ErrInvalidCandidate
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Mkdir(path, 0o700)
}

func hasRequiredFixtures(results []BenchmarkResult) bool {
	required := map[string]bool{"direct": false, "paraphrase": false, "near_miss": false, "adversarial": false}
	for _, result := range results {
		if _, ok := required[result.FixtureType]; ok && result.Passed {
			required[result.FixtureType] = true
		}
	}
	for _, passed := range required {
		if !passed {
			return false
		}
	}
	return true
}

// Rollback restores the most recent backup of a skill.
func (p *Promoter) Rollback(skillName string) (string, error) {
	if !validCandidateName(skillName) {
		return "", ErrInvalidCandidate
	}
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
	if !validCandidateName(skillName) {
		return "", ErrInvalidCandidate
	}
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
	info, err := os.Lstat(src)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return ErrInvalidCandidate
	}
	if info, err := os.Lstat(dst); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return ErrInvalidCandidate
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dst, 0o700); err != nil {
		return err
	}

	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalidCandidate
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return ErrInvalidCandidate
		}
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
	info, err := os.Lstat(src)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return ErrInvalidCandidate
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return ErrInvalidCandidate
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}
