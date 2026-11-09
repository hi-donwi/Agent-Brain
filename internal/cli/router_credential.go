package cli

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/gateway"
	"github.com/hi-donwi/Agent-Brain/internal/home"
	"github.com/hi-donwi/Agent-Brain/internal/router"
	"github.com/spf13/cobra"
)

func newRouterCredentialCmd() *cobra.Command {
	var protocols []string
	var output string
	cmd := &cobra.Command{
		Use:   "credential <profile-id>",
		Short: "Create a profile- and protocol-scoped inference credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			profileID := args[0]
			if !router.ProfileIDValid(profileID) {
				return fmt.Errorf("invalid routing profile ID")
			}
			if len(protocols) == 0 {
				return fmt.Errorf("at least one --protocol is required")
			}
			layout, err := home.Default()
			if err != nil {
				return err
			}
			if output == "" {
				output = filepath.Join(layout.Root, "agent-control.token")
			}
			if !filepath.IsAbs(output) {
				return fmt.Errorf("--output must be an absolute path")
			}
			config, err := router.LoadRouterConfig(filepath.Join(layout.Root, "accounts.yaml"))
			if err != nil {
				return err
			}
			profile, ok := router.FindRoutingProfile(config.RoutingProfiles, profileID)
			if !ok {
				return fmt.Errorf("routing profile %q is not configured", profileID)
			}
			allowed := make(map[string]bool)
			for _, target := range profile.Targets {
				for _, protocol := range target.Protocols {
					allowed[protocol] = true
				}
			}
			seen := make(map[string]bool)
			for _, protocol := range protocols {
				if !allowed[protocol] || seen[protocol] {
					return fmt.Errorf("protocol %q is unsupported or duplicated for routing profile %q", protocol, profileID)
				}
				seen[protocol] = true
			}
			localBackend, localEndpoint := "", ""
			for _, target := range profile.Targets {
				if router.NormalizeProvider(target.Provider) == router.ProviderLocal {
					localConfig, err := layout.LoadConfig()
					if err != nil {
						return fmt.Errorf("load local backend for profile revision: %w", err)
					}
					backendName, backendConfig := localConfig.PrimaryBackend()
					localBackend = backendName
					localEndpoint = backendConfig.Endpoint
					break
				}
			}
			revision, err := router.RoutingProfileRevisionFor(profile, config.Accounts, localBackend, localEndpoint)
			if err != nil {
				return err
			}
			master, err := readGatewayMaster(layout.Token)
			if err != nil {
				return err
			}
			token, err := gateway.NewInferenceToken(master, gateway.InferenceScope{ProfileID: profile.ID, Revision: revision, Protocols: protocols})
			if err != nil {
				return err
			}
			metadataPath := output + ".json"
			if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
				return fmt.Errorf("create credential directory: %w", err)
			}
			if err := writeExclusiveFile(output, token+"\n"); err != nil {
				return fmt.Errorf("write credential file: %w", err)
			}
			metadata, err := json.Marshal(struct {
				ID       string `json:"id"`
				Revision string `json:"revision"`
			}{ID: profile.ID, Revision: revision})
			if err != nil {
				_ = os.Remove(output)
				return fmt.Errorf("encode credential metadata: %w", err)
			}
			if err := writeExclusiveFile(metadataPath, string(metadata)+"\n"); err != nil {
				_ = os.Remove(output)
				return fmt.Errorf("write credential metadata: %w", err)
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "Created profile-scoped credential for %s (%s; %s) at %s\n", profile.ID, revision, strings.Join(protocols, ","), output)
			return err
		},
	}
	cmd.Flags().StringSliceVar(&protocols, "protocol", nil, "Allowed Brain protocol (repeatable)")
	cmd.Flags().StringVarP(&output, "output", "o", "", "New credential file path (default: <lokal-home>/agent-control.token; must not exist)")
	return cmd
}

func writeExclusiveFile(path, content string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.WriteString(content); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return err
	}
	return nil
}

func readGatewayMaster(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read gateway master token: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("gateway master token must be a regular owner-only file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read gateway master token: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if len(token) != 64 {
		return "", fmt.Errorf("gateway master token has an invalid format")
	}
	if _, err := hex.DecodeString(token); err != nil {
		return "", fmt.Errorf("gateway master token has an invalid format")
	}
	return token, nil
}
