package cli

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/router"
)

func TestRouterCredentialWritesOneProfileScopedTokenWithoutPrintingIt(t *testing.T) {
	root := t.TempDir()
	t.Setenv(home.EnvVar, root)
	master := strings.Repeat("a", 64)
	if _, err := hex.DecodeString(master); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "serve.token"), []byte(master+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := router.RoutingProfile{ID: "agent-control", Targets: []router.RoutingTarget{{
		Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "gpt-test",
		AccountIDs: []string{"account-a"}, Protocols: []string{router.ProtocolChat},
	}}}
	config := router.RouterConfig{Strategy: "priority", Accounts: []router.Account{{
		ID: "account-a", Provider: router.ProviderOpenAI, BaseURL: "https://api.example.invalid/v1",
		APIKey: "env:TEST_KEY", Enabled: true, Models: []string{"gpt-test"}, Protocols: []string{router.ProtocolChat},
	}}, RoutingProfiles: []router.RoutingProfile{profile}}
	if err := router.SaveRouterConfig(filepath.Join(root, "accounts.yaml"), config); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "agent-control.token")
	cmd := newRouterCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"credential", "agent-control", "--protocol", router.ProtocolChat, "--output", outputPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode is %o, want 600", info.Mode().Perm())
	}
	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" || strings.Contains(stdout.String()+stderr.String(), token) || strings.Contains(stdout.String()+stderr.String(), master) {
		t.Fatal("credential or gateway master token was empty or printed to command output")
	}
	revision, err := router.RoutingProfileRevision(profile, config.Accounts)
	if err != nil {
		t.Fatal(err)
	}
	scope, ok := gateway.VerifyInferenceToken(master, token)
	if !ok || scope.ProfileID != profile.ID || scope.Revision != revision || len(scope.Protocols) != 1 || scope.Protocols[0] != router.ProtocolChat {
		t.Fatalf("credential scope was not restricted to selected profile/protocol: %#v", scope)
	}
	metadata, err := os.ReadFile(outputPath + ".json")
	if err != nil || !strings.Contains(string(metadata), revision) || strings.Contains(string(metadata), token) {
		t.Fatal("profile metadata is missing or contains credential material")
	}
}

func TestRouterCredentialRefusesOverwrite(t *testing.T) {
	root := t.TempDir()
	t.Setenv(home.EnvVar, root)
	master := strings.Repeat("b", 64)
	if err := os.WriteFile(filepath.Join(root, "serve.token"), []byte(master+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := router.RoutingProfile{ID: "agent-control", Targets: []router.RoutingTarget{{
		Model: "assistant", Provider: router.ProviderOpenAI, UpstreamModel: "gpt-test",
		AccountIDs: []string{"account-a"}, Protocols: []string{router.ProtocolChat},
	}}}
	config := router.RouterConfig{Strategy: "priority", Accounts: []router.Account{{
		ID: "account-a", Provider: router.ProviderOpenAI, BaseURL: "https://api.example.invalid/v1", APIKey: "env:TEST_KEY", Enabled: true,
	}}, RoutingProfiles: []router.RoutingProfile{profile}}
	if err := router.SaveRouterConfig(filepath.Join(root, "accounts.yaml"), config); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(root, "existing.token")
	if err := os.WriteFile(outputPath, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := newRouterCmd()
	cmd.SetArgs([]string{"credential", profile.ID, "--protocol", router.ProtocolChat, "--output", outputPath})
	if err := cmd.Execute(); err == nil {
		t.Fatal("refused to protect an existing credential file")
	}
	data, err := os.ReadFile(outputPath)
	if err != nil || string(data) != "preserve" {
		t.Fatal("existing credential file was modified")
	}
}
