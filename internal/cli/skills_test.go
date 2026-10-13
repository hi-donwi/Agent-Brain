package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/skills"
)

func TestSkillsCLI(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "home")
	skillsDir := filepath.Join(homeDir, "skills")

	// Create test skill
	sampleSkill := filepath.Join(skillsDir, "sample-skill")
	if err := os.MkdirAll(filepath.Join(sampleSkill, "references"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	skillMD := `---
name: sample-skill
description: A sample skill for testing CLI subcommands.
metadata:
  pack: test
  keywords: sample, cli, test
---
# Process Instructions
1. Run step 1.
2. Run step 2.
`
	if err := os.WriteFile(filepath.Join(sampleSkill, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatalf("write SKILL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sampleSkill, "references", "guide.md"), []byte("Reference Guide Text"), 0o644); err != nil {
		t.Fatalf("write guide.md: %v", err)
	}

	deps := SkillsDeps{
		ResolveHome: func() (home.Layout, error) {
			return home.Layout{
				Root:   homeDir,
				Skills: skillsDir,
			}, nil
		},
		ResolveAdapter: func() (backend.Adapter, error) {
			return nil, nil
		},
	}

	// 1. Test "skills ls"
	t.Run("skills ls", func(t *testing.T) {
		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"ls"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("skills ls failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "sample-skill") || !strings.Contains(out, "test") {
			t.Errorf("unexpected ls output: %s", out)
		}
	})

	// 2. Test "skills ls --json"
	t.Run("skills ls --json", func(t *testing.T) {
		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"ls", "--json"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("skills ls --json failed: %v", err)
		}

		var list []skills.SkillSummary
		if err := json.Unmarshal(buf.Bytes(), &list); err != nil {
			t.Fatalf("failed to decode json: %v", err)
		}
		if len(list) != 1 || list[0].Name != "sample-skill" {
			t.Errorf("unexpected decoded list: %+v", list)
		}
	})

	// 3. Test "skills show"
	t.Run("skills show", func(t *testing.T) {
		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"show", "sample-skill"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("skills show failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "sample-skill") || !strings.Contains(out, "guide.md") {
			t.Errorf("unexpected show output: %s", out)
		}
	})

	// 4. Test "skills show --body"
	t.Run("skills show --body", func(t *testing.T) {
		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"show", "sample-skill", "--body"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("skills show --body failed: %v", err)
		}

		out := buf.String()
		if !strings.Contains(out, "1. Run step 1.") {
			t.Errorf("unexpected show body output: %s", out)
		}
	})

	// 5. Test "skills show --ref guide.md"
	t.Run("skills show --ref guide.md", func(t *testing.T) {
		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"show", "sample-skill", "--ref", "guide.md"})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("skills show --ref failed: %v", err)
		}

		out := buf.String()
		if out != "Reference Guide Text" {
			t.Errorf("unexpected reference output: %q", out)
		}
	})

	// 6. Test "skills validate"
	t.Run("skills validate valid", func(t *testing.T) {
		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"validate", sampleSkill})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("validate expected valid, got: %v", err)
		}
	})

	t.Run("skills validate invalid", func(t *testing.T) {
		invalidDir := filepath.Join(tmpDir, "invalid-skill")
		_ = os.MkdirAll(invalidDir, 0o755)
		_ = os.WriteFile(filepath.Join(invalidDir, "SKILL.md"), []byte("invalid content without frontmatter"), 0o644)

		cmd := newSkillsCmd(deps)
		errBuf := new(bytes.Buffer)
		cmd.SetErr(errBuf)
		cmd.SetArgs([]string{"validate", invalidDir})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for invalid skill validation")
		}
	})

	// 7. Test "skills add"
	t.Run("skills add", func(t *testing.T) {
		newSource := filepath.Join(tmpDir, "new-skill")
		_ = os.MkdirAll(newSource, 0o755)
		_ = os.WriteFile(filepath.Join(newSource, "SKILL.md"), []byte(`---
name: new-skill
description: Added skill via CLI.
---
# Process Body
`), 0o644)

		cmd := newSkillsCmd(deps)
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetArgs([]string{"add", newSource})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("skills add failed: %v", err)
		}

		if !strings.Contains(buf.String(), "Successfully installed skill \"new-skill\"") {
			t.Errorf("unexpected add output: %s", buf.String())
		}
	})
}
