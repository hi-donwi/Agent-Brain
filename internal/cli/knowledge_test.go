package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/cli"
	"github.com/hi-donwi/Agent-Brain/internal/home"
)

func TestKnowledgeSourceAddStoresCanonicalRootInPrivateConfig(t *testing.T) {
	root := t.TempDir()
	brainHome := filepath.Join(root, "brain-home")
	sourceRoot := filepath.Join(root, "workspace-docs")
	if err := os.MkdirAll(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(home.EnvVar, brainHome)
	layout, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := layout.SaveConfig(home.DefaultConfig("tiny", map[string]string{"chat": "qwen-test", "embed": "nomic-embed-text"})); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := cli.NewRootCmd()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"knowledge", "source", "add", "--id", "workspace_docs", "--name", "Workspace documents", "--root", sourceRoot, "--collection", "workspace_docs"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("add registered source: %v", err)
	}
	loaded, err := layout.LoadConfig()
	if err != nil || len(loaded.KnowledgeSources) != 1 {
		t.Fatalf("registered source missing from private config: %#v err=%v", loaded, err)
	}
	source := loaded.KnowledgeSources[0]
	if source.ID != "workspace_docs" || source.Name != "Workspace documents" || source.Collection != "workspace_docs" || source.Root != canonicalRoot || source.EmbeddingModel != "nomic-embed-text" {
		t.Fatalf("registered source fields = %+v", source)
	}
	if !bytes.Contains(out.Bytes(), []byte("workspace_docs")) || bytes.Contains(out.Bytes(), []byte(brainHome)) {
		t.Fatalf("source registration output is unexpected: %s", out.String())
	}
	info, err := os.Stat(layout.Config)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("source path config must stay owner-only: info=%v err=%v", info, err)
	}
}

func TestKnowledgeSourceAddRejectsBrainHomeAndFilesystemRoot(t *testing.T) {
	brainHome := t.TempDir()
	t.Setenv(home.EnvVar, brainHome)
	layout, err := home.Default()
	if err != nil {
		t.Fatal(err)
	}
	if err := layout.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := layout.SaveConfig(home.DefaultConfig("tiny", map[string]string{"chat": "qwen-test"})); err != nil {
		t.Fatal(err)
	}
	for _, sourceRoot := range []string{brainHome, string(filepath.Separator)} {
		cmd := cli.NewRootCmd()
		cmd.SetArgs([]string{"knowledge", "source", "add", "--id", "unsafe", "--name", "Unsafe root", "--root", sourceRoot, "--collection", "unsafe"})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("unsafe source root %q was accepted", sourceRoot)
		}
	}
	loaded, err := layout.LoadConfig()
	if err != nil || len(loaded.KnowledgeSources) != 0 {
		t.Fatalf("rejected roots changed config: %#v err=%v", loaded, err)
	}
}
