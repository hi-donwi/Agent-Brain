package forge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeCandidateForReview(t *testing.T, root, name, body string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	skill := "---\nname: " + name + "\ndescription: Use when running test helper workflows for code testing\n---\n# Test Helper\n\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o600); err != nil {
		t.Fatal(err)
	}
	provenance, err := yaml.Marshal(Provenance{ClusterID: "cluster-1", SourceSessions: []string{"session-1"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provenance.yaml"), provenance, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestPromotionRequiresHumanReviewAndFreshEvaluation(t *testing.T) {
	root := t.TempDir()
	candidatesDir := filepath.Join(root, "candidates")
	skillsDir := filepath.Join(root, "skills")
	candidateDir := writeCandidateForReview(t, candidatesDir, "test-helper", "Run tests and verify the results.")
	evaluator := NewEvaluator(candidatesDir, nil)
	promoter := NewPromoter(candidatesDir, skillsDir)
	if _, err := evaluator.EvaluateCandidate(context.Background(), "test-helper"); err != nil {
		t.Fatal(err)
	}
	if _, err := promoter.Promote("test-helper"); err == nil {
		t.Fatal("candidate promoted before provenance review")
	}
	review, err := ReviewCandidate(candidatesDir, "test-helper", false)
	if err != nil || !review.Reviewed {
		t.Fatalf("candidate review failed: %+v err=%v", review, err)
	}
	result, err := promoter.Promote("test-helper")
	if err != nil || result.ContentHash == "" {
		t.Fatalf("reviewed/evaluated candidate promotion failed: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "test-helper", "SKILL.md")); err != nil {
		t.Fatalf("promoted skill missing: %v", err)
	}

	if err := os.WriteFile(filepath.Join(candidateDir, "SKILL.md"), []byte("---\nname: test-helper\ndescription: Use when running test helper workflows for code testing\n---\n# Changed content\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := promoter.Promote("test-helper"); err == nil {
		t.Fatal("candidate promoted with stale evaluation/review evidence")
	}
}

func TestCandidateReviewRejectsUnsafeReferenceFiles(t *testing.T) {
	root := t.TempDir()
	candidatesDir := filepath.Join(root, "candidates")
	candidateDir := writeCandidateForReview(t, candidatesDir, "safe-helper", "Read the reference material carefully.")
	references := filepath.Join(candidateDir, "references")
	if err := os.Mkdir(references, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside.txt")
	if err := os.WriteFile(outside, []byte("synthetic-reference"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(references, "link.txt")); err != nil {
		t.Skipf("symlinks are unavailable: %v", err)
	}
	if _, err := ReviewCandidate(candidatesDir, "safe-helper", false); err == nil {
		t.Fatal("candidate with an escaping reference symlink passed review")
	}
}

func TestCandidateNameRejectsPathTraversal(t *testing.T) {
	if _, err := CandidateDirectory(t.TempDir(), "../outside"); err == nil {
		t.Fatal("candidate path traversal accepted")
	}
}
