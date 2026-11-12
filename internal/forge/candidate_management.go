package forge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/skills"
	"gopkg.in/yaml.v3"
)

var (
	ErrCandidateNotFound     = errors.New("forge candidate not found")
	ErrInvalidCandidate      = errors.New("invalid forge candidate")
	ErrCandidateNotReviewed  = errors.New("forge candidate provenance has not been reviewed")
	ErrCandidateNotEvaluated = errors.New("forge candidate does not have a fresh passing evaluation")
)

const (
	maxCandidateFileBytes  = 2 << 20
	maxCandidateTotalBytes = 10 << 20
)

type CandidateCheck struct {
	Name             string
	ContentHash      string
	StructuralPassed bool
	StructuralFields []string
	SafetyPassed     bool
	SafetyViolations []string
	Reviewed         bool
	ExternalContent  bool
	Provenance       *Provenance
	Evaluation       *CandidateEvalReport
	EvaluationPassed bool
	EvaluationFresh  bool
	PromotionReady   bool
}

func validCandidateName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name == "." || name == ".." || strings.HasPrefix(name, "-") || strings.HasSuffix(name, "-") {
		return false
	}
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' {
			continue
		}
		return false
	}
	return true
}

// CandidateAlias returns a stable, opaque alias suitable for an unscoped management view.
func CandidateAlias(master, name string) string {
	mac := hmac.New(sha256.New, []byte(master))
	_, _ = io.WriteString(mac, "brain-forge-candidate-v1\x00"+name)
	return "forge-" + hex.EncodeToString(mac.Sum(nil)[:12])
}

func CandidateNameForAlias(candidatesRoot, master, alias string) (string, error) {
	names, err := CandidateNames(candidatesRoot)
	if err != nil {
		return "", err
	}
	for _, name := range names {
		if hmac.Equal([]byte(CandidateAlias(master, name)), []byte(alias)) {
			return name, nil
		}
	}
	return "", ErrCandidateNotFound
}

func CandidateNames(candidatesRoot string) ([]string, error) {
	rootAbs, err := filepath.Abs(candidatesRoot)
	if err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !validCandidateName(entry.Name()) {
			continue
		}
		if _, err := CandidateDirectory(root, entry.Name()); err == nil {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// CandidateDirectory resolves one direct, non-symlink candidate child.
func CandidateDirectory(candidatesRoot, name string) (string, error) {
	if !validCandidateName(name) {
		return "", ErrInvalidCandidate
	}
	rootAbs, err := filepath.Abs(candidatesRoot)
	if err != nil {
		return "", ErrInvalidCandidate
	}
	root, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", ErrCandidateNotFound
	}
	root = filepath.Clean(root)
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", ErrCandidateNotFound
	}
	dir := filepath.Join(root, name)
	info, err = os.Lstat(dir)
	if err != nil {
		return "", ErrCandidateNotFound
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", ErrInvalidCandidate
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || filepath.Clean(resolved) != filepath.Clean(dir) || !candidatePathWithin(root, resolved) {
		return "", ErrInvalidCandidate
	}
	return resolved, nil
}

func candidatePathWithin(root, child string) bool {
	rel, err := filepath.Rel(root, child)
	return err == nil && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// ReadCandidateFile reads a bounded regular file under one candidate without following symlinks.
func ReadCandidateFile(candidatesRoot, name, relative string) ([]byte, error) {
	dir, err := CandidateDirectory(candidatesRoot, name)
	if err != nil {
		return nil, err
	}
	return skills.ReadSafeSkillFile(dir, relative, maxCandidateFileBytes)
}

// ScanCandidateFiles validates the candidate tree and secret-scans every promoted file.
// The returned hash covers only SKILL.md and references, not internal provenance/eval metadata.
func ScanCandidateFiles(dir string) (string, []string, error) {
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return "", nil, ErrInvalidCandidate
	}
	dir, err = filepath.EvalSymlinks(dirAbs)
	if err != nil {
		return "", nil, ErrCandidateNotFound
	}
	dir = filepath.Clean(dir)
	rootInfo, err := os.Stat(dir)
	if err != nil || !rootInfo.IsDir() {
		return "", nil, ErrInvalidCandidate
	}
	contentFiles := make(map[string][]byte)
	violations := make([]string, 0)
	var total int64
	seenSkill := false
	err = filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return ErrInvalidCandidate
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return ErrInvalidCandidate
		}
		if rel == "." {
			return nil
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidCandidate
		}
		if info.IsDir() {
			if rel != "references" && !strings.HasPrefix(rel, "references"+string(filepath.Separator)) {
				return ErrInvalidCandidate
			}
			return nil
		}
		allowed := rel == "SKILL.md" || rel == "provenance.yaml" || rel == "eval_report.json" || strings.HasPrefix(rel, "references"+string(filepath.Separator))
		if !allowed || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxCandidateFileBytes {
			return ErrInvalidCandidate
		}
		data, err := skills.ReadSafeSkillFile(dir, rel, maxCandidateFileBytes)
		if err != nil {
			return ErrInvalidCandidate
		}
		total += int64(len(data))
		if total > maxCandidateTotalBytes {
			return ErrInvalidCandidate
		}
		violations = append(violations, ScanSafety(data)...)
		if rel == "SKILL.md" {
			seenSkill = true
		}
		if rel == "SKILL.md" || strings.HasPrefix(rel, "references"+string(filepath.Separator)) {
			contentFiles[filepath.ToSlash(rel)] = data
		}
		return nil
	})
	if err != nil || !seenSkill {
		return "", nil, ErrInvalidCandidate
	}
	paths := make([]string, 0, len(contentFiles))
	for path := range contentFiles {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		_, _ = io.WriteString(hash, path)
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(contentFiles[path])
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), violations, nil
}

func InspectCandidate(candidatesRoot, name string) (*CandidateCheck, error) {
	dir, err := CandidateDirectory(candidatesRoot, name)
	if err != nil {
		return nil, err
	}
	hash, violations, err := ScanCandidateFiles(dir)
	if err != nil {
		return nil, err
	}
	validation := skills.ValidateDir(dir)
	fields := make([]string, 0, len(validation.Errors))
	for _, item := range validation.Errors {
		fields = append(fields, item.Field)
	}
	check := &CandidateCheck{
		Name: name, ContentHash: hash, StructuralPassed: validation.Valid,
		StructuralFields: fields, SafetyPassed: len(violations) == 0, SafetyViolations: violations,
	}
	if data, err := skills.ReadSafeSkillFile(dir, "provenance.yaml", maxCandidateFileBytes); err == nil {
		var provenance Provenance
		if yaml.Unmarshal(data, &provenance) == nil {
			check.Provenance = &provenance
			check.ExternalContent = provenance.ExternalContentIncluded
			check.Reviewed = provenance.SafetyChecked && provenance.ReviewedAt != nil && provenance.ReviewedContentHash == hash && len(provenance.SourceSessions) > 0
		}
	}
	if data, err := skills.ReadSafeSkillFile(dir, "eval_report.json", maxCandidateFileBytes); err == nil {
		var report CandidateEvalReport
		if json.Unmarshal(data, &report) == nil {
			check.Evaluation = &report
			check.EvaluationFresh = report.ContentHash != "" && report.ContentHash == hash
			check.EvaluationPassed = report.Passed && report.SafetyPassed && check.EvaluationFresh && hasRequiredFixtures(report.Benchmarks)
		}
	}
	check.PromotionReady = check.StructuralPassed && check.SafetyPassed && check.Reviewed && check.EvaluationPassed
	return check, nil
}

type CandidateReview struct {
	Name               string
	ContentHash        string
	ExternalContent    bool
	Reviewed           bool
	SourceSessionCount int
}

// ReviewCandidate records an explicit owner review after structural and safety gates pass.
func ReviewCandidate(candidatesRoot, name string, externalContentIncluded bool) (*CandidateReview, error) {
	dir, err := CandidateDirectory(candidatesRoot, name)
	if err != nil {
		return nil, err
	}
	validation := skills.ValidateDir(dir)
	if !validation.Valid {
		return nil, errors.New("candidate structure must validate before review")
	}
	hash, violations, err := ScanCandidateFiles(dir)
	if err != nil {
		return nil, err
	}
	if len(violations) > 0 {
		return nil, errors.New("candidate safety scan must pass before review")
	}
	data, err := skills.ReadSafeSkillFile(dir, "provenance.yaml", maxCandidateFileBytes)
	if err != nil {
		return nil, errors.New("candidate provenance is unavailable")
	}
	var provenance Provenance
	if err := yaml.Unmarshal(data, &provenance); err != nil || len(provenance.SourceSessions) == 0 {
		return nil, errors.New("candidate provenance is invalid or missing")
	}
	now := time.Now().UTC()
	provenance.SafetyChecked = true
	provenance.ExternalContentIncluded = externalContentIncluded
	provenance.ReviewedAt = &now
	provenance.ReviewedContentHash = hash
	updated, err := yaml.Marshal(provenance)
	if err != nil {
		return nil, fmt.Errorf("encode candidate provenance: %w", err)
	}
	if err := writeCandidateMetadata(dir, "provenance.yaml", updated); err != nil {
		return nil, err
	}
	return &CandidateReview{Name: name, ContentHash: hash, ExternalContent: externalContentIncluded, Reviewed: true, SourceSessionCount: len(provenance.SourceSessions)}, nil
}

func writeCandidateMetadata(dir, name string, data []byte) error {
	path := filepath.Join(dir, name)
	if info, err := os.Lstat(path); err == nil && (info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular()) {
		return ErrInvalidCandidate
	}
	return os.WriteFile(path, data, 0o600)
}
