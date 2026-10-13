package cli_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/cli"
)

func TestServiceCommand_Tree(t *testing.T) {
	root := cli.NewRootCmd()

	// Verify service command is registered
	cmd, _, err := root.Find([]string{"service"})
	if err != nil || cmd.Name() != "service" {
		t.Fatalf("lokal service command not found: %v", err)
	}

	subcommands := []string{"install", "uninstall", "status"}
	for _, sub := range subcommands {
		subCmd, _, err := root.Find([]string{"service", sub})
		if err != nil || subCmd.Name() != sub {
			t.Errorf("lokal service %s subcommand not found: %v", sub, err)
		}
	}
}

func TestServiceStatus_Help(t *testing.T) {
	root := cli.NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"service", "--help"})

	err := root.Execute()
	if err != nil {
		t.Fatalf("service --help failed: %v", err)
	}

	helpText := out.String()
	if !strings.Contains(helpText, "install") || !strings.Contains(helpText, "uninstall") || !strings.Contains(helpText, "status") {
		t.Errorf("expected subcommands in help output:\n%s", helpText)
	}
}
