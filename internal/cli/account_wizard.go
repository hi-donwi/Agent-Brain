package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func terminalInput(cmd *cobra.Command) bool {
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

type accountPrompts struct {
	reader *bufio.Reader
	output io.Writer
}

func (p accountPrompts) read(label, initial string) (string, error) {
	if initial != "" {
		fmt.Fprintf(p.output, "%s [%s]: ", label, initial)
	} else {
		fmt.Fprintf(p.output, "%s: ", label)
	}
	line, err := p.reader.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("account setup cancelled or input ended")
	}
	line = strings.TrimSpace(line)
	if line == "q" || line == "quit" {
		return "", fmt.Errorf("account setup cancelled")
	}
	if line == "" {
		line = initial
	}
	return line, nil
}
func (p accountPrompts) choose(label string, options []string) (string, error) {
	for {
		fmt.Fprintln(p.output, label)
		for i, option := range options {
			fmt.Fprintf(p.output, "  %d. %s\n", i+1, option)
		}
		value, err := p.read("Choose a number or ID (q to cancel)", "")
		if err != nil {
			return "", err
		}
		if index, err := strconv.Atoi(value); err == nil && index > 0 && index <= len(options) {
			return options[index-1], nil
		}
		for _, option := range options {
			if strings.EqualFold(value, option) {
				return option, nil
			}
		}
		fmt.Fprintln(p.output, "Choose one of the listed options.")
	}
}

func accountWizard(cmd *cobra.Command, a *router.Account) error {
	p := accountPrompts{bufio.NewReader(cmd.InOrStdin()), cmd.ErrOrStderr()}
	if a.Provider == "" {
		var options []string
		for _, provider := range router.Providers() {
			options = append(options, provider.ID)
		}
		value, err := p.choose("Provider", options)
		if err != nil {
			return err
		}
		a.Provider = value
	}
	definition, err := router.LookupProvider(a.Provider)
	if err != nil {
		return err
	}
	a.Provider = definition.ID
	if a.Provider == "opencode" {
		fmt.Fprintln(p.output, "OpenCode is a client. Select the provider account that OpenCode will use.")
		a.Provider = ""
		return accountWizard(cmd, a)
	}
	if definition.Note != "" {
		fmt.Fprintln(p.output, definition.Note)
	}
	if a.Auth == "" {
		a.Auth, err = p.choose("Authentication / account type", definition.Auth)
		if err != nil {
			return err
		}
	}
	if a.Name == "" {
		a.Name, err = p.read("Account name", "")
		if err != nil {
			return err
		}
	}
	if a.Auth == router.AuthAPIKey && a.APIKey == "" {
		if definition.KeyURL != "" {
			fmt.Fprintln(p.output, "Create or manage an API key at:", definition.KeyURL)
		}
		a.APIKey, err = p.read("Credential reference (env:NAME or file:PATH; q to cancel)", "")
		if err != nil {
			return err
		}
		if !strings.HasPrefix(a.APIKey, "env:") && !strings.HasPrefix(a.APIKey, "file:") {
			return fmt.Errorf("wizard accepts secret references only; use --key-stdin to store a literal key privately")
		}
	}
	if a.Auth == router.AuthOAuth {
		if a.BaseURL == "" {
			a.BaseURL, err = p.read("Dedicated bridge inference URL", "http://127.0.0.1:8317")
			if err != nil {
				return err
			}
		}
		if a.BridgeURL == "" {
			a.BridgeURL, err = p.read("Dedicated v8 management URL", strings.TrimRight(a.BaseURL, "/")+"/v8/management")
			if err != nil {
				return err
			}
		}
		if a.APIKey == "" {
			a.APIKey, err = p.read("Inference credential reference", "env:BRAIN_BRIDGE_KEY")
			if err != nil {
				return err
			}
		}
		if a.BridgeKey == "" {
			a.BridgeKey, err = p.read("Management credential reference", "env:BRAIN_BRIDGE_MANAGEMENT_KEY")
			if err != nil {
				return err
			}
		}
	}
	if (a.Provider == "custom" || a.Provider == "opencode-zen" || a.Provider == router.ProviderLocal) && a.BaseURL == "" {
		a.BaseURL, err = p.read("Inference base URL", definition.BaseURL)
		if err != nil {
			return err
		}
	}
	if (a.Provider == "custom" || a.Provider == "opencode-zen") && len(a.Protocols) == 0 {
		value, err := p.choose("Wire protocol", definition.Protocols)
		if err != nil {
			return err
		}
		a.Protocols = []string{value}
	}
	if len(definition.Protocols) > 0 && len(a.Models) == 0 {
		value, err := p.read("Allowed model IDs (comma separated; blank to discover later)", "")
		if err != nil {
			return err
		}
		if value != "" {
			a.Models = strings.Split(value, ",")
		}
	}
	return nil
}
