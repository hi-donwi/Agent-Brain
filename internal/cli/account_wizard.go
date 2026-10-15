package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func terminalInput(cmd *cobra.Command) bool {
	file, ok := cmd.InOrStdin().(*os.File)
	if !ok {
		return false
	}
	return isatty.IsTerminal(file.Fd()) || isatty.IsCygwinTerminal(file.Fd())
}

type accountPrompts struct {
	reader   *bufio.Reader
	output   io.Writer
	terminal *os.File
}

func (p accountPrompts) credential() (string, error) {
	const label = "API key (paste key, env:NAME or file:PATH; q to cancel)"
	var value string
	var err error
	if p.terminal == nil {
		value, err = p.read(label, "")
	} else {
		fd := int(p.terminal.Fd())
		state, rawErr := term.MakeRaw(fd)
		if rawErr != nil {
			return "", fmt.Errorf("cannot hide credential input; use --key-stdin or --key env:NAME")
		}
		defer term.Restore(fd, state)
		terminal := term.NewTerminal(struct {
			io.Reader
			io.Writer
		}{p.reader, p.output}, "")
		value, err = terminal.ReadPassword(label + ": ")
	}
	if err != nil && err != term.ErrPasteIndicator {
		return "", fmt.Errorf("account setup cancelled or credential input ended")
	}
	// x/term's line editor caps input at 4096 runes. Reject a full line so a
	// pasted credential is never silently saved after terminal truncation.
	if len(value) >= 4096 {
		return "", fmt.Errorf("credential must be shorter than 4096 bytes")
	}
	value = strings.TrimSpace(value)
	if value == "q" || value == "quit" {
		return "", fmt.Errorf("account setup cancelled")
	}
	if value == "" {
		return "", fmt.Errorf("credential is required")
	}
	if strings.ContainsFunc(value, unicode.IsControl) {
		return "", fmt.Errorf("credential must be a single line without control characters")
	}
	return value, nil
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
	p := accountPrompts{reader: bufio.NewReader(cmd.InOrStdin()), output: cmd.ErrOrStderr()}
	if terminalInput(cmd) {
		p.terminal = cmd.InOrStdin().(*os.File)
	}
	var definition router.Provider
	var err error
	for {
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
		definition, err = router.LookupProvider(a.Provider)
		if err != nil {
			return err
		}
		a.Provider = definition.ID
		if a.Provider != "opencode" {
			break
		}
		fmt.Fprintln(p.output, "OpenCode is a client. Select the provider account that OpenCode will use.")
		a.Provider = ""
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
		a.APIKey, err = p.credential()
		if err != nil {
			return err
		}
	}
	if a.Auth == router.AuthOAuth && (a.BaseURL != "" || a.BridgeURL != "" || a.APIKey != "" || a.BridgeKey != "" || externalBridgeRequested(cmd)) {
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
	return nil
}

func externalBridgeRequested(cmd *cobra.Command) bool {
	value, _ := cmd.Flags().GetBool("external-bridge")
	return value
}
