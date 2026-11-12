package gateway

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/forge"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"gopkg.in/yaml.v3"
)

func writeManagementCandidate(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: Use when running test helper workflows for code testing\n---\n# Test helper\nRun tests and verify the results.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	provenance, err := yaml.Marshal(forge.Provenance{ClusterID: "synthetic-cluster", SourceSessions: []string{"synthetic-source-session"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "provenance.yaml"), provenance, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestManagementForgeInventoryUsesOpaqueIDsAndPromotionGates(t *testing.T) {
	brainHome := t.TempDir()
	t.Setenv(home.EnvVar, brainHome)
	layout, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	writeManagementCandidate(t, layout.Candidates, "test-helper")
	writeManagementCandidate(t, layout.Candidates, "external-helper")
	server, err := NewServer(ServerConfig{Token: "gateway-master"})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	alias := forge.CandidateAlias("gateway-master", "test-helper")
	list := managementRequest(server, http.MethodGet, "/manage/v1/forge/candidates", "")
	for _, private := range []string{brainHome, "test-helper", "external-helper", "synthetic-source-session"} {
		if strings.Contains(list.Body.String(), private) {
			t.Fatalf("Forge inventory leaked private candidate data %q: %s", private, list.Body.String())
		}
	}
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), alias) {
		t.Fatalf("Forge inventory missing opaque ID: %d %s", list.Code, list.Body.String())
	}
	validate := managementRequest(server, http.MethodPost, "/manage/v1/forge/candidates/"+alias+"/validation", `{"confirmation":"validate `+alias+`"}`)
	if validate.Code != http.StatusOK || !strings.Contains(validate.Body.String(), `"safety_passed":true`) {
		t.Fatalf("candidate validation failed: %d %s", validate.Code, validate.Body.String())
	}
	evaluate := managementRequest(server, http.MethodPost, "/manage/v1/forge/candidates/"+alias+"/evaluation", `{"confirmation":"evaluate `+alias+`"}`)
	if evaluate.Code != http.StatusOK || !strings.Contains(evaluate.Body.String(), `"evaluation_passed":true`) {
		t.Fatalf("candidate evaluation failed: %d %s", evaluate.Code, evaluate.Body.String())
	}
	premature := managementRequest(server, http.MethodPost, "/manage/v1/forge/candidates/"+alias+"/promotion", `{"confirmation":"promote `+alias+`"}`)
	if premature.Code != http.StatusConflict || !strings.Contains(premature.Body.String(), "promotion_gates_failed") {
		t.Fatalf("unreviewed candidate was promoted: %d %s", premature.Code, premature.Body.String())
	}
	if _, err := forge.ReviewCandidate(layout.Candidates, "test-helper", false); err != nil {
		t.Fatal(err)
	}
	externalAlias := forge.CandidateAlias("gateway-master", "external-helper")
	if _, err := forge.NewEvaluator(layout.Candidates, nil).EvaluateCandidate(context.Background(), "external-helper"); err != nil {
		t.Fatal(err)
	}
	if _, err := forge.ReviewCandidate(layout.Candidates, "external-helper", true); err != nil {
		t.Fatal(err)
	}
	externalPromotion := managementRequest(server, http.MethodPost, "/manage/v1/forge/candidates/"+externalAlias+"/promotion", `{"confirmation":"promote `+externalAlias+`"}`)
	if externalPromotion.Code != http.StatusConflict || !strings.Contains(externalPromotion.Body.String(), "external_candidate_cli_only") {
		t.Fatalf("external-content candidate was promoted through unscoped Settings: %d %s", externalPromotion.Code, externalPromotion.Body.String())
	}
	promoted := managementRequest(server, http.MethodPost, "/manage/v1/forge/candidates/"+alias+"/promotion", `{"confirmation":"promote `+alias+`"}`)
	if promoted.Code != http.StatusOK || strings.Contains(promoted.Body.String(), "test-helper") || strings.Contains(promoted.Body.String(), brainHome) {
		t.Fatalf("reviewed candidate promotion failed or leaked paths/names: %d %s", promoted.Code, promoted.Body.String())
	}
	if _, err := os.Stat(filepath.Join(layout.Skills, "test-helper", "SKILL.md")); err != nil {
		t.Fatalf("Brain-user skill was not promoted: %v", err)
	}
	discarded := managementRequest(server, http.MethodDelete, "/manage/v1/forge/candidates/"+externalAlias, `{"confirmation":"discard `+externalAlias+`"}`)
	if discarded.Code != http.StatusOK {
		t.Fatalf("candidate discard failed: %d %s", discarded.Code, discarded.Body.String())
	}
}
