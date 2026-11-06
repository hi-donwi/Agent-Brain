package agent

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const contractVectorFile = "testdata/agent-policy-contract-v1.json"

type contractVectors struct {
	SchemaVersion int                     `json:"schema_version"`
	Instructions  []instructionVector     `json:"instructions"`
	Permissions   []permissionVector      `json:"permissions"`
	InvalidPolicy []invalidPolicyVector   `json:"invalid_policies"`
	Subagents     []subagentVector        `json:"subagents"`
	InvalidAgents []invalidSubagentVector `json:"invalid_subagents"`
}

type instructionVector struct {
	ID        string            `json:"id"`
	Files     map[string]string `json:"files"`
	Directory string            `json:"directory"`
	Expected  []struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Truncated bool   `json:"truncated"`
	} `json:"expected_files"`
}

type permissionVector struct {
	ID                  string            `json:"id"`
	PolicyYAML          string            `json:"policy_yaml"`
	TrustedPolicyYAML   string            `json:"trusted_policy_yaml"`
	UntrustedPolicyYAML string            `json:"untrusted_policy_yaml"`
	Request             permissionRequest `json:"request"`
	Expected            Decision          `json:"expected"`
}

type invalidPolicyVector struct {
	ID         string `json:"id"`
	PolicyYAML string `json:"policy_yaml"`
}

type permissionRequest struct {
	Tool    string `json:"tool"`
	Path    string `json:"path"`
	Command string `json:"command"`
}

type subagentVector struct {
	ID               string            `json:"id"`
	File             string            `json:"file"`
	Definition       string            `json:"definition"`
	ParentPolicyYAML string            `json:"parent_policy_yaml"`
	Expected         expectedSubagent  `json:"expected_definition"`
	Requests         []subagentRequest `json:"requests"`
}

type invalidSubagentVector struct {
	ID         string `json:"id"`
	File       string `json:"file"`
	Definition string `json:"definition"`
}

type expectedSubagent struct {
	Name     string   `json:"name"`
	Tools    []string `json:"tools"`
	Model    string   `json:"model"`
	MaxTurns int      `json:"max_turns"`
}

type subagentRequest struct {
	Request  permissionRequest `json:"request"`
	Expected Decision          `json:"expected"`
}

func loadContractVectors(t *testing.T) contractVectors {
	t.Helper()
	data, err := os.ReadFile(contractVectorFile)
	if err != nil {
		t.Fatalf("read shared contract vectors: %v", err)
	}

	var vectors contractVectors
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&vectors); err != nil {
		t.Fatalf("decode shared contract vectors: %v", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf("decode trailing contract vector data: got %v, want end of file", err)
	}
	if vectors.SchemaVersion != 1 {
		t.Fatalf("schema_version = %d, want 1", vectors.SchemaVersion)
	}
	if len(vectors.Instructions) == 0 || len(vectors.Permissions) == 0 || len(vectors.InvalidPolicy) == 0 || len(vectors.Subagents) == 0 || len(vectors.InvalidAgents) == 0 {
		t.Fatal("each contract vector group must contain at least one case")
	}
	checkVectorIDs(t, "instructions", instructionIDs(vectors.Instructions))
	checkVectorIDs(t, "permissions", permissionIDs(vectors.Permissions))
	checkVectorIDs(t, "invalid_policies", invalidPolicyIDs(vectors.InvalidPolicy))
	checkVectorIDs(t, "subagents", subagentIDs(vectors.Subagents))
	checkVectorIDs(t, "invalid_subagents", invalidSubagentIDs(vectors.InvalidAgents))
	return vectors
}

func checkVectorIDs(t *testing.T, group string, ids []string) {
	t.Helper()
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if id == "" {
			t.Fatalf("%s vector has an empty id", group)
		}
		if _, exists := seen[id]; exists {
			t.Fatalf("%s vector id %q is duplicated", group, id)
		}
		seen[id] = struct{}{}
	}
}

func instructionIDs(vectors []instructionVector) []string {
	ids := make([]string, 0, len(vectors))
	for _, vector := range vectors {
		ids = append(ids, vector.ID)
	}
	return ids
}

func permissionIDs(vectors []permissionVector) []string {
	ids := make([]string, 0, len(vectors))
	for _, vector := range vectors {
		ids = append(ids, vector.ID)
	}
	return ids
}

func invalidPolicyIDs(vectors []invalidPolicyVector) []string {
	ids := make([]string, 0, len(vectors))
	for _, vector := range vectors {
		ids = append(ids, vector.ID)
	}
	return ids
}

func subagentIDs(vectors []subagentVector) []string {
	ids := make([]string, 0, len(vectors))
	for _, vector := range vectors {
		ids = append(ids, vector.ID)
	}
	return ids
}

func invalidSubagentIDs(vectors []invalidSubagentVector) []string {
	ids := make([]string, 0, len(vectors))
	for _, vector := range vectors {
		ids = append(ids, vector.ID)
	}
	return ids
}

func TestSharedInstructionContractVectors(t *testing.T) {
	for _, vector := range loadContractVectors(t).Instructions {
		t.Run(vector.ID, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range vector.Files {
				writeContractFixture(t, root, path, content)
			}
			directory := filepath.Join(root, filepath.FromSlash(vector.Directory))
			if err := os.MkdirAll(directory, 0o755); err != nil {
				t.Fatal(err)
			}

			got, err := LoadInstructions(root, directory)
			if err != nil {
				t.Fatalf("LoadInstructions: %v", err)
			}
			if len(got.Files) != len(vector.Expected) {
				t.Fatalf("got %d instruction files, want %d: %+v", len(got.Files), len(vector.Expected), got.Files)
			}
			for i, want := range vector.Expected {
				actual := got.Files[i]
				if actual.Rel != want.Path || actual.Content != want.Content || actual.Truncated != want.Truncated {
					t.Errorf("file %d = {path:%q content:%q truncated:%t}, want {path:%q content:%q truncated:%t}",
						i, actual.Rel, actual.Content, actual.Truncated, want.Path, want.Content, want.Truncated)
				}
			}
		})
	}
}

func TestSharedPermissionContractVectors(t *testing.T) {
	for _, vector := range loadContractVectors(t).Permissions {
		t.Run(vector.ID, func(t *testing.T) {
			root := t.TempDir()
			policy, err := vectorPolicy(vector, root)
			if err != nil {
				t.Fatalf("load vector policy: %v", err)
			}
			request := Request{Tool: vector.Request.Tool, Path: vector.Request.Path, Command: vector.Request.Command}
			if got := policy.Decide(request); got != vector.Expected {
				t.Errorf("decision = %q, want %q", got, vector.Expected)
			}
		})
	}
}

func TestSharedInvalidPolicyContractVectors(t *testing.T) {
	for _, vector := range loadContractVectors(t).InvalidPolicy {
		t.Run(vector.ID, func(t *testing.T) {
			if _, err := ParsePolicy([]byte(vector.PolicyYAML)); err == nil {
				t.Fatal("ParsePolicy succeeded, want a validation error")
			}
		})
	}
}

func TestSharedSubagentContractVectors(t *testing.T) {
	for _, vector := range loadContractVectors(t).Subagents {
		t.Run(vector.ID, func(t *testing.T) {
			definition, err := ParseDefinition(vector.File, []byte(vector.Definition))
			if err != nil {
				t.Fatalf("ParseDefinition: %v", err)
			}
			gotDefinition := expectedSubagent{
				Name: definition.Name, Tools: definition.Tools,
				Model: definition.Model, MaxTurns: definition.MaxTurns,
			}
			if !reflect.DeepEqual(gotDefinition, vector.Expected) {
				t.Errorf("definition = %+v, want %+v", gotDefinition, vector.Expected)
			}

			parent, err := ParsePolicy([]byte(vector.ParentPolicyYAML))
			if err != nil {
				t.Fatalf("ParsePolicy(parent): %v", err)
			}
			parent.Root = t.TempDir()
			effective := definition.Effective(parent)
			for i, request := range vector.Requests {
				got := effective.Decide(Request{
					Tool: request.Request.Tool, Path: request.Request.Path, Command: request.Request.Command,
				})
				if got != request.Expected {
					t.Errorf("request %d (%s) decision = %q, want %q", i, request.Request.Tool, got, request.Expected)
				}
			}
		})
	}
}

func TestSharedInvalidSubagentContractVectors(t *testing.T) {
	for _, vector := range loadContractVectors(t).InvalidAgents {
		t.Run(vector.ID, func(t *testing.T) {
			if _, err := ParseDefinition(vector.File, []byte(vector.Definition)); err == nil {
				t.Fatal("ParseDefinition succeeded, want a validation error")
			}
		})
	}
}

func vectorPolicy(vector permissionVector, root string) (Policy, error) {
	if vector.TrustedPolicyYAML != "" || vector.UntrustedPolicyYAML != "" {
		if vector.TrustedPolicyYAML == "" || vector.UntrustedPolicyYAML == "" || vector.PolicyYAML != "" {
			return Policy{}, fmt.Errorf("vector must set either policy_yaml or both layered policy documents")
		}
		trusted, err := ParsePolicy([]byte(vector.TrustedPolicyYAML))
		if err != nil {
			return Policy{}, fmt.Errorf("parse trusted policy: %w", err)
		}
		untrusted, err := ParsePolicy([]byte(vector.UntrustedPolicyYAML))
		if err != nil {
			return Policy{}, fmt.Errorf("parse untrusted policy: %w", err)
		}
		trusted.Root = root
		policy := Layer(trusted, untrusted)
		policy.Root = root
		return policy, nil
	}
	if vector.PolicyYAML == "" {
		return Policy{}, fmt.Errorf("vector must set policy_yaml or both layered policy documents")
	}
	policy, err := ParsePolicy([]byte(vector.PolicyYAML))
	if err != nil {
		return Policy{}, err
	}
	policy.Root = root
	return policy, nil
}

func writeContractFixture(t *testing.T, root, slashPath, content string) {
	t.Helper()
	if filepath.IsAbs(slashPath) || slashPath == "" {
		t.Fatalf("fixture path must be relative: %q", slashPath)
	}
	path := filepath.Join(root, filepath.FromSlash(slashPath))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Fatalf("fixture path escapes root: %q", slashPath)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
