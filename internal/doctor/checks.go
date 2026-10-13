package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/catalog"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
)

// The lokal home holds the index database, traces, and skills, not models, which
// live with their backend and are checked against model sizes separately.
const (
	homeDiskWarnBelow = 2 << 30
	homeDiskFailBelow = 512 << 20
)

// Default is the check list `lokal doctor` runs on this host.
func Default() []Check {
	defaultLayout := home.Default
	defaultConfig := func() (*home.Config, error) {
		l, err := defaultLayout()
		if err != nil {
			return nil, err
		}
		return l.LoadConfig()
	}
	defaultAdapter := func() (backend.Adapter, error) {
		cfg, err := defaultConfig()
		if err == nil && cfg != nil {
			engine, bCfg := cfg.PrimaryBackend()
			return backend.NewAdapter(engine, bCfg.Endpoint, bCfg.Token, nil)
		}
		return backend.NewOllama(backend.DefaultOllamaEndpoint, nil), nil
	}

	return []Check{
		HardwareCheck(hw.Detect),
		HomeCheck(defaultLayout),
		HomeDiskCheck(defaultLayout, hw.FreeBytes),
		ConfigCheck(defaultConfig),
		BackendCheck(defaultConfig, defaultAdapter),
		ModelsCheck(defaultConfig, defaultAdapter, catalog.Default()),
		ContextMemoryCheck(defaultConfig, catalog.Default(), hw.Detect),
		SmokeCheck(defaultConfig, defaultAdapter),
	}
}

var fixHomeEnv = fmt.Sprintf("set %s to an absolute path, or unset it to use ~/.lokal", home.EnvVar)

// HardwareCheck summarises what detection found and surfaces its warnings.
func HardwareCheck(detect func() (hw.Info, error)) Check {
	return Check{ID: "hardware", Title: "Hardware", Run: func(context.Context) Outcome {
		info, err := detect()
		if errors.Is(err, errors.ErrUnsupported) {
			return Outcome{Severity: Warn, Message: err.Error() + "; the profile cannot be chosen automatically"}
		}
		if err != nil {
			return Outcome{Severity: Fail, Message: "hardware detection failed: " + err.Error()}
		}
		msg := describe(info)
		if len(info.Warnings) > 0 {
			return Outcome{Severity: Warn, Message: msg + "; " + strings.Join(info.Warnings, "; ")}
		}
		return Outcome{Severity: OK, Message: msg}
	}}
}

func describe(info hw.Info) string {
	model := info.CPU.Model
	if model == "" {
		model = "unknown CPU"
	}
	avail := "available unknown"
	if info.Memory.AvailableBytes > 0 {
		avail = HumanBytes(info.Memory.AvailableBytes) + " available"
	}
	accel := info.Accelerator
	if accel == "" {
		accel = hw.AcceleratorNone
	}
	s := fmt.Sprintf("%s, %d logical CPUs, %s memory (%s), accelerator %s",
		model, info.CPU.Logical, HumanBytes(info.Memory.TotalBytes), avail, accel)
	if info.Virtualization.Guest {
		s += fmt.Sprintf(", VM (%s)", info.Virtualization.Hypervisor)
	}
	for _, g := range info.GPUs {
		s += fmt.Sprintf(", GPU %s (%s)", g.Name, HumanBytes(g.MemoryBytes))
	}
	return s
}

// HomeCheck verifies the lokal home resolves, is a writable directory when it
// exists, and is not open to other users.
func HomeCheck(resolve func() (home.Layout, error)) Check {
	return Check{ID: "home", Title: "lokal home", Run: func(context.Context) Outcome {
		l, err := resolve()
		if err != nil {
			return Outcome{Severity: Fail, Message: err.Error(), Fix: fixHomeEnv}
		}
		st, err := os.Stat(l.Root)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return Outcome{Severity: OK, Message: l.Root + " not created yet; lokal setup creates it"}
		case err != nil:
			return Outcome{Severity: Fail, Message: err.Error()}
		case !st.IsDir():
			return Outcome{Severity: Fail, Message: l.Root + " exists but is not a directory",
				Fix: "move it away, or " + fixHomeEnv}
		}
		probe, err := os.CreateTemp(l.Root, ".doctor-*")
		if err != nil {
			return Outcome{Severity: Fail, Message: l.Root + " is not writable: " + err.Error(),
				Fix: "chmod u+rwx " + l.Root}
		}
		name := probe.Name()
		probe.Close()
		os.Remove(name)
		if runtime.GOOS != "windows" && st.Mode().Perm()&0o077 != 0 {
			return Outcome{Severity: Warn,
				Message: fmt.Sprintf("%s is accessible by other users (mode %04o); it holds the gateway token and traces", l.Root, st.Mode().Perm()),
				Fix:     "chmod 700 " + l.Root}
		}
		return Outcome{Severity: OK, Message: l.Root + " is writable"}
	}}
}

// HomeDiskCheck verifies there is room for the index database and traces.
func HomeDiskCheck(resolve func() (home.Layout, error), freeBytes func(string) (uint64, error)) Check {
	return Check{ID: "disk", Title: "Free disk", Run: func(context.Context) Outcome {
		l, err := resolve()
		if err != nil {
			return Outcome{Severity: Fail, Message: err.Error(), Fix: fixHomeEnv}
		}
		free, err := freeBytes(l.Root)
		if errors.Is(err, errors.ErrUnsupported) {
			return Outcome{Severity: Warn, Message: "free disk space cannot be measured on this OS"}
		}
		if err != nil {
			return Outcome{Severity: Fail, Message: err.Error()}
		}
		msg := fmt.Sprintf("%s free on the filesystem holding %s", HumanBytes(free), l.Root)
		fix := "free disk space, or " + fixHomeEnv + " on a larger disk"
		switch {
		case free < homeDiskFailBelow:
			return Outcome{Severity: Fail, Message: msg + ", below the " + HumanBytes(homeDiskFailBelow) + " minimum", Fix: fix}
		case free < homeDiskWarnBelow:
			return Outcome{Severity: Warn, Message: msg + ", below the recommended " + HumanBytes(homeDiskWarnBelow), Fix: fix}
		}
		return Outcome{Severity: OK, Message: msg}
	}}
}

// ConfigCheck checks if config.yaml exists and is valid.
func ConfigCheck(resolveConfig func() (*home.Config, error)) Check {
	return Check{ID: "config", Title: "Configuration", Run: func(context.Context) Outcome {
		cfg, err := resolveConfig()
		if errors.Is(err, os.ErrNotExist) {
			return Outcome{
				Severity: Warn,
				Message:  "config.yaml not created yet; lokal setup creates it",
				Fix:      "lokal setup",
			}
		}
		if err != nil {
			return Outcome{
				Severity: Fail,
				Message:  "invalid configuration: " + err.Error(),
				Fix:      "check config.yaml or run lokal setup",
			}
		}
		return Outcome{
			Severity: OK,
			Message:  fmt.Sprintf("profile %s, %d model aliases configured", cfg.Profile, len(cfg.Aliases)),
		}
	}}
}

// BackendCheck verifies that the inference backend is reachable and responsive.
func BackendCheck(resolveConfig func() (*home.Config, error), resolveAdapter func() (backend.Adapter, error)) Check {
	return Check{ID: "backend", Title: "Inference backend", Run: func(ctx context.Context) Outcome {
		ad, err := resolveAdapter()
		if err != nil {
			return Outcome{Severity: Fail, Message: "cannot initialize adapter: " + err.Error()}
		}

		ver, err := ad.Version(ctx)
		if err != nil {
			fix := "start the backend (e.g. brew services start ollama or systemctl start ollama)"
			switch ad.Name() {
			case "remote", "gateway":
				fix = "ensure the remote host or SSH tunnel is active (e.g. ssh -N -L 52140:127.0.0.1:52140 <vm>)"
			case "llamacpp":
				fix = "start llama-server (e.g. llama-server -m <model.gguf> --port 8080)"
			case "vllm":
				fix = "start vLLM (e.g. vllm serve <model> --port 8000)"
			case "lmstudio":
				fix = "start LM Studio and enable the local inference server"
			default:
				if runtime.GOOS == "darwin" {
					fix = "brew services start ollama (or open /Applications/Ollama.app)"
				}
			}

			// If config is not yet created, report as warning rather than failure
			cfg, cfgErr := resolveConfig()
			if cfgErr != nil || cfg == nil {
				return Outcome{
					Severity: Warn,
					Message:  fmt.Sprintf("%s not reachable at %s; start it before running lokal setup", ad.Name(), ad.Endpoint()),
					Fix:      fix,
				}
			}

			return Outcome{
				Severity: Fail,
				Message:  fmt.Sprintf("%s unreachable at %s: %v", ad.Name(), ad.Endpoint(), err),
				Fix:      fix,
			}
		}

		return Outcome{
			Severity: OK,
			Message:  fmt.Sprintf("%s %s reachable at %s", ad.Name(), ver, ad.Endpoint()),
		}
	}}
}

// ModelsCheck verifies that every configured alias resolves to an installed model in the backend.
func ModelsCheck(resolveConfig func() (*home.Config, error), resolveAdapter func() (backend.Adapter, error), cat *catalog.Catalog) Check {
	return Check{ID: "models", Title: "Configured models", Run: func(ctx context.Context) Outcome {
		if cat == nil {
			cat = catalog.Default()
		}
		cfg, err := resolveConfig()
		if err != nil {
			return Outcome{Severity: Warn, Message: "models check skipped: config not ready"}
		}
		ad, err := resolveAdapter()
		if err != nil {
			return Outcome{Severity: Warn, Message: "models check skipped: backend adapter not ready"}
		}
		installed, err := ad.ListModels(ctx)
		if err != nil {
			return Outcome{Severity: Warn, Message: "cannot query installed models: backend offline"}
		}

		var missing []string
		for alias, modelID := range cfg.Aliases {
			backendID := modelID
			if m, ok := cat.GetModel(modelID); ok {
				if bID := m.Backends[ad.Name()]; bID != "" {
					backendID = bID
				}
			}

			found := false
			for _, m := range installed {
				if m.Name == modelID || m.Name == backendID || strings.HasPrefix(m.Name, modelID+":") || strings.HasPrefix(m.Name, backendID+":") || strings.HasPrefix(backendID, m.Name) {
					found = true
					break
				}
			}
			if !found {
				missing = append(missing, fmt.Sprintf("%s (%s)", alias, modelID))
			}
		}

		if len(missing) > 0 {
			return Outcome{
				Severity: Fail,
				Message:  fmt.Sprintf("missing models: %s", strings.Join(missing, ", ")),
				Fix:      "lokal setup (or ollama pull <model>)",
			}
		}

		return Outcome{
			Severity: OK,
			Message:  fmt.Sprintf("all %d aliases resolve to installed models", len(cfg.Aliases)),
		}
	}}
}

// ContextMemoryCheck checks whether configured models fit within available memory.
func ContextMemoryCheck(resolveConfig func() (*home.Config, error), cat *catalog.Catalog, detectHW func() (hw.Info, error)) Check {
	return Check{ID: "memory", Title: "Context memory budget", Run: func(context.Context) Outcome {
		cfg, err := resolveConfig()
		if err != nil {
			return Outcome{Severity: Warn, Message: "memory budget check skipped: config not ready"}
		}
		info, err := detectHW()
		if err != nil {
			return Outcome{Severity: Warn, Message: "memory budget check skipped: " + err.Error()}
		}

		var totalMem uint64
		dedup := make(map[string]bool)
		for _, modelID := range cfg.Aliases {
			if dedup[modelID] {
				continue
			}
			dedup[modelID] = true
			if m, ok := cat.GetModel(modelID); ok {
				totalMem += m.MemoryBytes
			}
		}

		availMem := info.Memory.TotalBytes
		if totalMem > availMem && availMem > 0 {
			return Outcome{
				Severity: Warn,
				Message:  fmt.Sprintf("configured models require %s, which exceeds host RAM %s", HumanBytes(totalMem), HumanBytes(availMem)),
				Fix:      "switch to a smaller profile (lokal setup --profile tiny)",
			}
		}

		return Outcome{
			Severity: OK,
			Message:  fmt.Sprintf("configured models (%s) fit within memory budget (%s)", HumanBytes(totalMem), HumanBytes(availMem)),
		}
	}}
}

// SmokeCheck performs a quick live chat and embedding request to verify end-to-end inference.
func SmokeCheck(resolveConfig func() (*home.Config, error), resolveAdapter func() (backend.Adapter, error)) Check {
	return Check{ID: "smoke", Title: "Inference smoke test", Run: func(ctx context.Context) Outcome {
		cfg, err := resolveConfig()
		if err != nil {
			return Outcome{Severity: Warn, Message: "smoke test skipped: config not ready"}
		}
		ad, err := resolveAdapter()
		if err != nil {
			return Outcome{Severity: Warn, Message: "smoke test skipped: backend not ready"}
		}

		chatModel := cfg.Aliases["chat"]
		if chatModel == "" {
			return Outcome{Severity: Warn, Message: "smoke test skipped: no chat alias configured"}
		}
		backendChatModel := chatModel
		if m, ok := catalog.Default().GetModel(chatModel); ok {
			if bID := m.Backends[ad.Name()]; bID != "" {
				backendChatModel = bID
			}
		}

		thinkFalse := false
		chatResp, err := ad.Chat(ctx, backend.ChatRequest{
			Model: backendChatModel,
			Messages: []backend.ChatMessage{
				{Role: "user", Content: "ping"},
			},
			Think:   &thinkFalse,
			Options: &backend.ChatOptions{NumCtx: 2048},
		})
		if err != nil {
			return Outcome{
				Severity: Fail,
				Message:  fmt.Sprintf("chat smoke test failed on %s: %v", chatModel, err),
				Fix:      "check backend logs or verify model installation",
			}
		}

		embedModel := cfg.Aliases["embed"]
		if embedModel != "" {
			backendEmbedModel := embedModel
			if m, ok := catalog.Default().GetModel(embedModel); ok {
				if bID := m.Backends[ad.Name()]; bID != "" {
					backendEmbedModel = bID
				}
			}
			truncateFalse := false
			_, err := ad.Embed(ctx, backend.EmbedRequest{
				Model:    backendEmbedModel,
				Input:    []string{"test"},
				Truncate: &truncateFalse,
			})
			if err != nil {
				return Outcome{
					Severity: Fail,
					Message:  fmt.Sprintf("embed smoke test failed on %s: %v", embedModel, err),
					Fix:      "verify embedding model in backend",
				}
			}
		}

		return Outcome{
			Severity: OK,
			Message:  fmt.Sprintf("chat on %s: %.1f tok/s, embedding OK", chatModel, chatResp.TokensPerSecond),
		}
	}}
}
