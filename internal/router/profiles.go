package router

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var profileIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
var profileModelPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/-]{0,255}$`)

// ProfileIDValid reports whether id is a stable, URL/header-safe routing profile key.
func ProfileIDValid(id string) bool { return profileIDPattern.MatchString(id) }

type resolvedProfileTarget struct {
	Model         string              `json:"model"`
	Provider      string              `json:"provider"`
	UpstreamModel string              `json:"upstream_model"`
	Protocols     []string            `json:"protocols"`
	Accounts      []resolvedAccountID `json:"accounts,omitempty"`
}

type resolvedAccountID struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
	BaseURL  string `json:"base_url"`
}

type resolvedProfileRevision struct {
	ID           string                  `json:"id"`
	LocalBackend *resolvedLocalBackend   `json:"local_backend,omitempty"`
	Targets      []resolvedProfileTarget `json:"targets"`
}

type resolvedLocalBackend struct {
	Name     string `json:"name"`
	Endpoint string `json:"endpoint"`
}

// ValidateRoutingProfiles verifies explicit targets and account references before serving.
func ValidateRoutingProfiles(profiles []RoutingProfile, accounts []Account) error {
	if len(profiles) > 100 {
		return fmt.Errorf("routing profiles exceed the limit of 100")
	}
	accountByID := make(map[string]Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	profileIDs := make(map[string]bool, len(profiles))
	for _, profile := range profiles {
		if !profileIDPattern.MatchString(profile.ID) || profileIDs[profile.ID] {
			return fmt.Errorf("routing profiles contain an invalid or duplicate ID")
		}
		profileIDs[profile.ID] = true
		if len(profile.Targets) == 0 || len(profile.Targets) > 100 {
			return fmt.Errorf("routing profile %q must contain between 1 and 100 targets", profile.ID)
		}
		models := make(map[string]bool, len(profile.Targets))
		for _, target := range profile.Targets {
			if !profileModelPattern.MatchString(target.Model) || models[target.Model] {
				return fmt.Errorf("routing profile %q contains an invalid or duplicate model ID", profile.ID)
			}
			models[target.Model] = true
			if !profileModelPattern.MatchString(target.UpstreamModel) {
				return fmt.Errorf("routing profile %q target %q has an invalid upstream model", profile.ID, target.Model)
			}
			provider := NormalizeProvider(target.Provider)
			if provider == "" {
				return fmt.Errorf("routing profile %q target %q must name a provider", profile.ID, target.Model)
			}
			definition, err := LookupProvider(provider)
			if err != nil {
				return fmt.Errorf("routing profile %q target %q: %w", profile.ID, target.Model, err)
			}
			if len(target.Protocols) == 0 {
				return fmt.Errorf("routing profile %q target %q must declare protocols", profile.ID, target.Model)
			}
			seenProtocols := make(map[string]bool, len(target.Protocols))
			for _, protocol := range target.Protocols {
				if protocol == "" || seenProtocols[protocol] || !contains(definition.Protocols, protocol) {
					return fmt.Errorf("routing profile %q target %q has an unsupported or duplicate protocol", profile.ID, target.Model)
				}
				if protocol == ProtocolGemini && (strings.Contains(target.Model, ":") || strings.Contains(target.UpstreamModel, ":")) {
					return fmt.Errorf("routing profile %q Gemini targets cannot contain path delimiters", profile.ID)
				}
				seenProtocols[protocol] = true
			}
			if provider == ProviderLocal {
				if len(target.AccountIDs) != 0 {
					return fmt.Errorf("routing profile %q local target %q cannot name accounts", profile.ID, target.Model)
				}
				for _, protocol := range target.Protocols {
					if protocol != ProtocolChat && protocol != ProtocolEmbeddings {
						return fmt.Errorf("routing profile %q local target %q has an unsupported protocol", profile.ID, target.Model)
					}
				}
				continue
			}
			if len(target.AccountIDs) == 0 || len(target.AccountIDs) > 100 {
				return fmt.Errorf("routing profile %q target %q must name explicit candidate accounts", profile.ID, target.Model)
			}
			seenAccounts := make(map[string]bool, len(target.AccountIDs))
			for _, accountID := range target.AccountIDs {
				account, ok := accountByID[accountID]
				if !ok || seenAccounts[accountID] || NormalizeProvider(account.Provider) != provider {
					return fmt.Errorf("routing profile %q target %q references an invalid account", profile.ID, target.Model)
				}
				seenAccounts[accountID] = true
				if len(account.Models) > 0 && !contains(account.Models, target.UpstreamModel) {
					return fmt.Errorf("routing profile %q target %q is outside account %q model allowlist", profile.ID, target.Model, accountID)
				}
				for _, protocol := range target.Protocols {
					if !account.Supports(protocol) {
						return fmt.Errorf("routing profile %q target %q account %q does not support protocol %q", profile.ID, target.Model, accountID, protocol)
					}
				}
			}
		}
		localTarget := false
		for _, target := range profile.Targets {
			localTarget = localTarget || NormalizeProvider(target.Provider) == ProviderLocal
		}
		if !localTarget {
			if _, err := RoutingProfileRevision(profile, accounts); err != nil {
				return fmt.Errorf("routing profile %q: %w", profile.ID, err)
			}
		}
	}
	return nil
}

// RoutingProfileRevision fingerprints the declared targets and their resolved endpoint identities.
// Secrets and mutable account health are deliberately excluded.
func RoutingProfileRevision(profile RoutingProfile, accounts []Account) (string, error) {
	return RoutingProfileRevisionFor(profile, accounts, "", "")
}

// RoutingProfileRevisionFor also binds local targets to the configured loopback backend.
func RoutingProfileRevisionFor(profile RoutingProfile, accounts []Account, localBackend, localEndpoint string) (string, error) {
	accountByID := make(map[string]Account, len(accounts))
	for _, account := range accounts {
		accountByID[account.ID] = account
	}
	revision := resolvedProfileRevision{ID: profile.ID, Targets: make([]resolvedProfileTarget, 0, len(profile.Targets))}
	for _, target := range profile.Targets {
		if NormalizeProvider(target.Provider) == ProviderLocal {
			if strings.TrimSpace(localBackend) == "" || ValidateLoopbackEndpoint(localEndpoint) != nil {
				return "", fmt.Errorf("local routing profile requires a loopback backend endpoint")
			}
			local := strings.TrimRight(localEndpoint, "/")
			revision.LocalBackend = &resolvedLocalBackend{Name: localBackend, Endpoint: local}
			protocols := append([]string(nil), target.Protocols...)
			sort.Strings(protocols)
			revision.Targets = append(revision.Targets, resolvedProfileTarget{
				Model: target.Model, Provider: ProviderLocal, UpstreamModel: target.UpstreamModel,
				Protocols: protocols,
			})
		}
	}
	for _, target := range profile.Targets {
		if NormalizeProvider(target.Provider) == ProviderLocal {
			continue
		}
		resolved := resolvedProfileTarget{
			Model: target.Model, Provider: NormalizeProvider(target.Provider), UpstreamModel: target.UpstreamModel,
			Protocols: append([]string(nil), target.Protocols...),
		}
		sort.Strings(resolved.Protocols)
		ids := append([]string(nil), target.AccountIDs...)
		sort.Strings(ids)
		for _, id := range ids {
			account, ok := accountByID[id]
			if !ok {
				return "", fmt.Errorf("target references an unknown account")
			}
			provider, err := LookupProvider(account.Provider)
			if err != nil {
				return "", err
			}
			baseURL := strings.TrimRight(account.BaseURL, "/")
			if baseURL == "" {
				baseURL = strings.TrimRight(provider.BaseURL, "/")
			}
			if baseURL == "" || ValidateEndpoint(baseURL) != nil {
				return "", fmt.Errorf("target account has no valid effective endpoint")
			}
			resolved.Accounts = append(resolved.Accounts, resolvedAccountID{ID: id, Provider: NormalizeProvider(account.Provider), BaseURL: baseURL})
		}
		revision.Targets = append(revision.Targets, resolved)
	}
	sort.Slice(revision.Targets, func(i, j int) bool { return revision.Targets[i].Model < revision.Targets[j].Model })
	data, err := json.Marshal(revision)
	if err != nil {
		return "", fmt.Errorf("encode routing profile revision: %w", err)
	}
	digest := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

// FindRoutingProfile returns a profile by its exact ID.
func FindRoutingProfile(profiles []RoutingProfile, id string) (RoutingProfile, bool) {
	for _, profile := range profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return RoutingProfile{}, false
}

// FindRoutingTarget returns the exact model/protocol target in a profile.
func FindRoutingTarget(profile RoutingProfile, model, protocol string) (RoutingTarget, bool) {
	for _, target := range profile.Targets {
		if target.Model == model && contains(target.Protocols, protocol) {
			return target, true
		}
	}
	return RoutingTarget{}, false
}
