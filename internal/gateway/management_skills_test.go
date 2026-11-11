package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/home"
)

func mustJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestManagementSkillImportValidatesScansAndAtomicallyInstalls(t *testing.T) {
	brainHome := t.TempDir()
	t.Setenv(home.EnvVar, brainHome)
	layout, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	skillsRoot, err := canonicalBrainHomeChild(layout.Root, layout.Skills)
	if err != nil {
		t.Fatalf("resolve initial skills root: %v", err)
	}
	if err := os.MkdirAll(skillsRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := canonicalBrainHomeChild(layout.Root, skillsRoot); err != nil {
		t.Fatalf("validate initialized skills root: %v", err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	skill := "---\nname: local-test-helper\ndescription: Use when running tests and verifying results\n---\n# Test helper\n\nRun the relevant tests and verify the output.\n"
	response := managementRequest(server, http.MethodPost, "/manage/v1/skills", `{"name":"local-test-helper","skill_md":`+mustJSON(skill)+`,"confirmation":"install local-test-helper"}`)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"installed":true`) || strings.Contains(response.Body.String(), skill) || strings.Contains(response.Body.String(), brainHome) {
		t.Fatalf("valid skill import failed or returned private content/path: %d %s", response.Code, response.Body.String())
	}
	installed := filepath.Join(brainHome, "skills", "local-test-helper", "SKILL.md")
	data, err := os.ReadFile(installed)
	if err != nil || string(data) != skill {
		t.Fatalf("installed skill file mismatch: err=%v data=%q", err, data)
	}
	info, err := os.Stat(installed)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("skill content must be owner-only: info=%v err=%v", info, err)
	}

	duplicate := managementRequest(server, http.MethodPost, "/manage/v1/skills", `{"name":"local-test-helper","skill_md":`+mustJSON(skill)+`,"confirmation":"install local-test-helper"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate skill import returned %d: %s", duplicate.Code, duplicate.Body.String())
	}
}

func TestManagementSkillImportRejectsTraversalMalformedAndSecretContent(t *testing.T) {
	brainHome := t.TempDir()
	t.Setenv(home.EnvVar, brainHome)
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	for _, test := range []struct {
		name, body, confirm string
		want                int
		code                string
	}{
		{"../escape", "---\nname: escape\ndescription: Use when testing helper workflows\n---\n", "install ../escape", http.StatusBadRequest, "bad_request"},
		{"not-matching", "---\nname: embedded-name\ndescription: Use when testing helper workflows\n---\n", "install not-matching", http.StatusBadRequest, "invalid_skill"},
		{"secret-skill", "---\nname: secret-skill\ndescription: Use when testing helper workflows\n---\npassword: \"synthetic-private-secret\"\n", "install secret-skill", http.StatusBadRequest, "skill_safety_rejected"},
		{"large-skill", "---\nname: large-skill\ndescription: Use when testing helper workflows\n---\n" + strings.Repeat("x", 70<<10), "install large-skill", http.StatusRequestEntityTooLarge, "payload_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			body := `{"name":` + mustJSON(test.name) + `,"skill_md":` + mustJSON(test.body) + `,"confirmation":` + mustJSON(test.confirm) + `}`
			response := managementRequest(server, http.MethodPost, "/manage/v1/skills", body)
			if response.Code != test.want || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("unsafe skill import returned %d, expected %d/%s: %s", response.Code, test.want, test.code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "synthetic-private-secret") || strings.Contains(response.Body.String(), brainHome) {
				t.Fatalf("skill import error leaked sensitive data: %s", response.Body.String())
			}
		})
	}
}

func TestManagementSkillsAreUserScopedRedactedAndRemovable(t *testing.T) {
	base := t.TempDir()
	brainHome := filepath.Join(base, "brain-home")
	userSkills := filepath.Join(brainHome, "skills", "user-skill")
	configuredSkill := filepath.Join(base, "configured", "configured-skill")
	if err := os.MkdirAll(userSkills, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(configuredSkill, 0o700); err != nil {
		t.Fatal(err)
	}
	userBody := "Private instructions shown only in the Brain-origin console."
	for path, content := range map[string]string{
		filepath.Join(userSkills, "SKILL.md"): `---
name: user-skill
description: Brain user skill.
license: Apache-2.0
---
# Instructions
` + userBody,
		filepath.Join(configuredSkill, "SKILL.md"): `---
name: configured-skill
description: A configured external skill that is not exposed in the unscoped console.
---
# External
`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv(home.EnvVar, brainHome)
	layout, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	config := home.DefaultConfig("tiny", map[string]string{"chat": "qwen-test"})
	config.SkillSources = []string{filepath.Join(base, "configured")}
	if err := layout.SaveConfig(config); err != nil {
		t.Fatal(err)
	}
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())

	list := managementRequest(server, http.MethodGet, "/manage/v1/skills", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), brainHome) || strings.Contains(list.Body.String(), "configured-skill") {
		t.Fatalf("skill list leaked a path/configured source: %d %s", list.Code, list.Body.String())
	}
	if !strings.Contains(list.Body.String(), "user-skill") {
		t.Fatalf("user skill missing from list: %s", list.Body.String())
	}

	detail := managementRequest(server, http.MethodGet, "/manage/v1/skills/user-skill", "")
	if detail.Code != http.StatusOK || !strings.Contains(detail.Body.String(), userBody) || strings.Contains(detail.Body.String(), brainHome) {
		t.Fatalf("skill detail failed or exposed root: %d %s", detail.Code, detail.Body.String())
	}
	validation := managementRequest(server, http.MethodGet, "/manage/v1/skills/user-skill/validation", "")
	var result struct {
		Valid bool `json:"valid"`
	}
	if validation.Code != http.StatusOK || json.Unmarshal(validation.Body.Bytes(), &result) != nil || !result.Valid || strings.Contains(validation.Body.String(), brainHome) {
		t.Fatalf("skill validation failed or exposed root: %d %s", validation.Code, validation.Body.String())
	}
	wrongConfirmation := managementRequest(server, http.MethodDelete, "/manage/v1/skills/user-skill", `{"confirmation":"wrong"}`)
	if wrongConfirmation.Code != http.StatusBadRequest {
		t.Fatalf("skill removal without exact confirmation returned %d", wrongConfirmation.Code)
	}
	removed := managementRequest(server, http.MethodDelete, "/manage/v1/skills/user-skill", `{"confirmation":"remove user-skill"}`)
	if removed.Code != http.StatusOK {
		t.Fatalf("user skill removal returned %d: %s", removed.Code, removed.Body.String())
	}
	if _, err := os.Stat(userSkills); !os.IsNotExist(err) {
		t.Fatalf("user skill directory remained after removal: %v", err)
	}
	if _, err := os.Stat(filepath.Join(configuredSkill, "SKILL.md")); err != nil {
		t.Fatalf("configured skill was modified: %v", err)
	}
}

func TestManagementMCPInventoryIsReadOnlyStdioAndDoesNotStartAProcess(t *testing.T) {
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	response := managementRequest(server, http.MethodGet, "/manage/v1/mcp", "")
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"transport":"stdio"`) || !strings.Contains(response.Body.String(), `"started_by_console":false`) || !strings.Contains(response.Body.String(), "search_knowledge") {
		t.Fatalf("MCP inventory response is invalid: %d %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "tool arguments") || strings.Contains(response.Body.String(), "rootDir") {
		t.Fatalf("MCP inventory included project/process configuration: %s", response.Body.String())
	}
}
