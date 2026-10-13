package skills_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/skills"
)

func TestParseSkillMD(t *testing.T) {
	content := []byte(`---
name: test-skill
description: A helpful skill for automated testing workflows.
license: Apache-2.0
metadata:
  pack: core
  keywords: testing, unit tests, automation
---

# Test Skill

This is the body of the skill.
`)

	fm, body, err := skills.ParseSkillMD(content)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	if fm.Name != "test-skill" {
		t.Errorf("expected name 'test-skill', got %q", fm.Name)
	}
	if fm.Description != "A helpful skill for automated testing workflows." {
		t.Errorf("unexpected description: %q", fm.Description)
	}
	if fm.License != "Apache-2.0" {
		t.Errorf("unexpected license: %q", fm.License)
	}
	if body != "# Test Skill\n\nThis is the body of the skill." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestParseSkillMD_Errors(t *testing.T) {
	// Missing leading delimiter
	_, _, err := skills.ParseSkillMD([]byte("name: missing\n---\nbody"))
	if err == nil {
		t.Error("expected error for missing leading delimiter")
	}

	// Missing closing delimiter
	_, _, err = skills.ParseSkillMD([]byte("---\nname: missing\nbody without closing delimiter"))
	if err == nil {
		t.Error("expected error for missing closing delimiter")
	}
}

func TestValidate(t *testing.T) {
	validSkill := &skills.Skill{
		Frontmatter: skills.Frontmatter{
			Name:        "valid-skill",
			Description: "A valid skill description within length limit.",
		},
		Body: "# Process\n1. Step one.",
	}

	res := skills.Validate(validSkill, "valid-skill")
	if !res.Valid {
		t.Fatalf("expected valid skill, got errors: %s", res.Error())
	}

	// Name mismatch
	res = skills.Validate(validSkill, "wrong-dir-name")
	if res.Valid {
		t.Error("expected invalid when skill name does not match dirName")
	}

	// Uppercase or illegal chars in name
	invalidNameSkill := &skills.Skill{
		Frontmatter: skills.Frontmatter{
			Name:        "Invalid_Skill",
			Description: "Desc",
		},
		Body: "Body",
	}
	res = skills.Validate(invalidNameSkill, "Invalid_Skill")
	if res.Valid {
		t.Error("expected invalid for uppercase/underscores in name")
	}

	// Empty body
	emptyBodySkill := &skills.Skill{
		Frontmatter: skills.Frontmatter{
			Name:        "empty-body",
			Description: "Desc",
		},
		Body: "",
	}
	res = skills.Validate(emptyBodySkill, "empty-body")
	if res.Valid {
		t.Error("expected invalid for empty body")
	}

	// Unsafe reference
	unsafeRefSkill := &skills.Skill{
		Frontmatter: skills.Frontmatter{
			Name:        "unsafe-ref",
			Description: "Desc",
		},
		Body:       "Body",
		References: []string{"../secret.txt"},
	}
	res = skills.Validate(unsafeRefSkill, "unsafe-ref")
	if res.Valid {
		t.Error("expected invalid for reference path traversal")
	}
}

func TestManagerPrecedence(t *testing.T) {
	tmpDir := t.TempDir()

	projDir := filepath.Join(tmpDir, "project")
	userDir := filepath.Join(tmpDir, "user")
	cfgDir := filepath.Join(tmpDir, "configured")

	// Create project skill: git-flow (version in project)
	projSkillDir := filepath.Join(projDir, ".agents", "skills", "git-flow")
	mustWriteFile(t, filepath.Join(projSkillDir, "SKILL.md"), `---
name: git-flow
description: Project override for git flow.
metadata:
  pack: project
---
# Project Git Flow Body
`)

	// Create user skill: git-flow (version in user) and deploy-flow
	userSkillDir1 := filepath.Join(userDir, "git-flow")
	mustWriteFile(t, filepath.Join(userSkillDir1, "SKILL.md"), `---
name: git-flow
description: User version of git flow.
metadata:
  pack: user
---
# User Git Flow Body
`)

	userSkillDir2 := filepath.Join(userDir, "deploy-flow")
	mustWriteFile(t, filepath.Join(userSkillDir2, "SKILL.md"), `---
name: deploy-flow
description: User deployment flow.
metadata:
  pack: user
---
# Deploy Flow Body
`)
	// Add a reference to deploy-flow
	mustWriteFile(t, filepath.Join(userSkillDir2, "references", "guide.md"), "Guide Content")

	// Create configured skill: cloud-flow
	cfgSkillDir := filepath.Join(cfgDir, "cloud-flow")
	mustWriteFile(t, filepath.Join(cfgSkillDir, "SKILL.md"), `---
name: cloud-flow
description: Cloud orchestration flow.
metadata:
  pack: cloud
---
# Cloud Flow Body
`)

	mgr := skills.NewManager(projDir, userDir, []string{cfgDir})

	list, err := mgr.List()
	if err != nil {
		t.Fatalf("list error: %v", err)
	}

	if len(list) != 3 {
		t.Fatalf("expected 3 deduplicated skills, got %d", len(list))
	}

	// git-flow must be resolved from project, not user
	skill, err := mgr.GetSkill("git-flow")
	if err != nil {
		t.Fatalf("get git-flow error: %v", err)
	}
	if skill.Summary.Source != skills.SourceProject {
		t.Errorf("expected git-flow from project, got %s", skill.Summary.Source)
	}
	if skill.Summary.Description != "Project override for git flow." {
		t.Errorf("expected project description, got %q", skill.Summary.Description)
	}

	// Check reference loading
	refContent, err := mgr.GetReference("deploy-flow", "guide.md")
	if err != nil {
		t.Fatalf("get reference error: %v", err)
	}
	if refContent != "Guide Content" {
		t.Errorf("unexpected ref content: %q", refContent)
	}

	// Unsafe reference path check
	_, err = mgr.GetReference("deploy-flow", "../guide.md")
	if err == nil {
		t.Error("expected error for traversal in reference path")
	}
}

func TestManagerAddSkill(t *testing.T) {
	tmpDir := t.TempDir()
	userDir := filepath.Join(tmpDir, "user")
	srcDir := filepath.Join(tmpDir, "source", "new-skill")

	mustWriteFile(t, filepath.Join(srcDir, "SKILL.md"), `---
name: new-skill
description: Newly authored skill.
---
# New Skill Body
`)

	mgr := skills.NewManager("", userDir, nil)

	added, err := mgr.AddSkill(srcDir)
	if err != nil {
		t.Fatalf("add skill failed: %v", err)
	}
	if added.Summary.Name != "new-skill" {
		t.Errorf("expected name new-skill, got %s", added.Summary.Name)
	}

	// Verify it now exists in user directory
	found, err := mgr.GetSkill("new-skill")
	if err != nil {
		t.Fatalf("get added skill failed: %v", err)
	}
	if found.Summary.Source != skills.SourceUser {
		t.Errorf("expected source user, got %s", found.Summary.Source)
	}
}

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(ctx context.Context, model string, text string) ([]float32, error) {
	// Simple deterministic fake embedding: vector with dim 4
	vec := []float32{0.1, 0.2, 0.3, 0.4}
	if text == "cloud" || text == "cloud-flow: Cloud orchestration flow." {
		vec = []float32{0.9, 0.1, 0.0, 0.0}
	}
	return vec, nil
}

func TestRouter(t *testing.T) {
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "skills")

	s1 := filepath.Join(skillDir, "unit-test")
	mustWriteFile(t, filepath.Join(s1, "SKILL.md"), `---
name: unit-test
description: Write fast unit tests and assertions.
metadata:
  keywords: unit test, tdd, assertions
---
# Unit Test Body
`)

	s2 := filepath.Join(skillDir, "cloud-flow")
	mustWriteFile(t, filepath.Join(s2, "SKILL.md"), `---
name: cloud-flow
description: Cloud orchestration flow.
metadata:
  keywords: cloud, kubernetes, docker
---
# Cloud Flow Body
`)

	mgr := skills.NewManager("", skillDir, nil)
	embedder := &fakeEmbedder{}
	router := skills.NewRouter(mgr, embedder, "test-embed")

	ctx := context.Background()

	// Direct keyword search
	res, err := router.Route(ctx, "run unit tests", 1000)
	if err != nil {
		t.Fatalf("route error: %v", err)
	}
	if len(res) == 0 {
		t.Fatal("expected at least 1 match")
	}
	if res[0].Summary.Name != "unit-test" {
		t.Errorf("expected top match unit-test, got %s", res[0].Summary.Name)
	}

	// Budget constraint: tiny budget allows only 1 skill
	resTiny, err := router.Route(ctx, "run unit tests", 15)
	if err != nil {
		t.Fatalf("tiny budget route error: %v", err)
	}
	if len(resTiny) > 1 {
		t.Errorf("expected at most 1 skill under tiny token budget, got %d", len(resTiny))
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write file %s: %v", path, err)
	}
}
