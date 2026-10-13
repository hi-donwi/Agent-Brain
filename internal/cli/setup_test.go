package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/doctor"
	"github.com/hi-donwi/Agent-Local/internal/home"
	"github.com/hi-donwi/Agent-Local/internal/hw"
	"github.com/hi-donwi/Agent-Local/internal/setup"
)

const gib = 1024 * 1024 * 1024

func mockSetupDeps(freeDisk uint64, backendReady bool) SetupDeps {
	return SetupDeps{
		DetectHW: func() (hw.Info, error) {
			return hw.Info{
				OS:          "darwin",
				Arch:        "arm64",
				Accelerator: hw.AcceleratorMetal,
				Memory:      hw.Memory{TotalBytes: 8 * gib},
			}, nil
		},
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return "/tmp/.lokal" }, nil)
		},
		FreeBytes: func(string) (uint64, error) {
			return freeDisk, nil
		},
		CheckBackend: func(context.Context) (bool, []string, error) {
			if backendReady {
				return true, []string{"nomic-embed-text:latest"}, nil
			}
			return false, nil, nil
		},
	}
}

func TestSetupCmd_DryRun(t *testing.T) {
	deps := mockSetupDeps(20*gib, true)
	cmd := newSetupCmd(deps)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Hardware profile: tiny") {
		t.Errorf("expected tiny profile in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Inference backend: ollama (running)") {
		t.Errorf("expected running backend in output, got:\n%s", out)
	}
	if !strings.Contains(out, "[dry-run] Plan calculated successfully") {
		t.Errorf("expected dry-run footer in output, got:\n%s", out)
	}
}

func TestSetupCmd_JSON(t *testing.T) {
	deps := mockSetupDeps(20*gib, false)
	cmd := newSetupCmd(deps)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--dry-run", "--json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	var plan setup.Plan
	if err := json.Unmarshal(buf.Bytes(), &plan); err != nil {
		t.Fatalf("decode json output failed: %v\nOutput: %s", err, buf.String())
	}

	if plan.Profile != "tiny" {
		t.Errorf("expected tiny profile, got %s", plan.Profile)
	}
	if !plan.DiskSufficient {
		t.Error("expected DiskSufficient=true with 20 GiB free")
	}
	if plan.Backend.Installed {
		t.Error("expected Backend.Installed=false")
	}
}

func TestSetupCmd_InsufficientDisk(t *testing.T) {
	deps := mockSetupDeps(500*1024*1024, false) // 500 MiB free
	cmd := newSetupCmd(deps)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--dry-run"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error with insufficient disk space, got nil")
	}

	exitErr, ok := err.(*ExitCodeError)
	if !ok || exitErr.Code != 2 {
		t.Errorf("expected ExitCodeError with code 2, got: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "INSUFFICIENT DISK") {
		t.Errorf("expected INSUFFICIENT DISK notice in output, got:\n%s", out)
	}
}

func TestSetupCmd_ProfileOverride(t *testing.T) {
	deps := mockSetupDeps(30*gib, true)
	cmd := newSetupCmd(deps)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--dry-run", "--profile", "standard"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "Hardware profile: standard") {
		t.Errorf("expected standard profile in output, got:\n%s", out)
	}
}

func TestSetupCmd_Yes_Success(t *testing.T) {
	tempHome := t.TempDir()
	adapter := &mockCLIAdapter{}
	doctorRan := false

	deps := SetupDeps{
		DetectHW: func() (hw.Info, error) {
			return hw.Info{
				OS:          "darwin",
				Arch:        "arm64",
				Accelerator: hw.AcceleratorMetal,
				Memory:      hw.Memory{TotalBytes: 8 * gib},
			}, nil
		},
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
		FreeBytes: func(string) (uint64, error) {
			return 50 * gib, nil
		},
		CheckBackend: func(context.Context) (bool, []string, error) {
			return true, nil, nil
		},
		ResolveAdapter: func() (backend.Adapter, error) {
			return adapter, nil
		},
		RunDoctor: func(context.Context) (doctor.Report, error) {
			doctorRan = true
			return doctor.Report{
				Checks: []doctor.Result{
					{ID: "backend", Title: "backend", Severity: doctor.OK, Message: "ollama running"},
				},
				Summary: doctor.Summary{OK: 1},
			}, nil
		},
	}

	cmd := newSetupCmd(deps)
	var outBuf bytes.Buffer
	cmd.SetOut(&outBuf)
	cmd.SetArgs([]string{"--yes"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() failed: %v", err)
	}

	// Verify model was pulled
	if len(adapter.pulled) == 0 {
		t.Error("expected models to be pulled, got 0")
	}

	// Verify config.yaml was written
	cfg, err := home.LoadConfig(tempHome + "/config.yaml")
	if err != nil {
		t.Fatalf("failed to load generated config: %v", err)
	}
	if cfg.Profile != "tiny" {
		t.Errorf("expected profile tiny, got %s", cfg.Profile)
	}

	// Verify doctor ran
	if !doctorRan {
		t.Error("expected doctor to run after setup")
	}

	if !strings.Contains(outBuf.String(), "Setup completed successfully") {
		t.Errorf("expected completion message, got: %s", outBuf.String())
	}
}

func TestSetupCmd_Yes_BackendNotRunning(t *testing.T) {
	deps := mockSetupDeps(20*gib, false)
	cmd := newSetupCmd(deps)

	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--yes"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when backend is not running")
	}
	var exit *ExitCodeError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
	if !strings.Contains(errBuf.String(), "Cannot proceed unattended") {
		t.Errorf("expected unattended notice in stderr, got: %s", errBuf.String())
	}
}

func TestSetupCmd_Yes_PullFailure(t *testing.T) {
	tempHome := t.TempDir()
	adapter := &mockCLIAdapter{pullErr: errors.New("network error")}

	deps := SetupDeps{
		DetectHW: func() (hw.Info, error) {
			return hw.Info{
				OS:          "darwin",
				Arch:        "arm64",
				Accelerator: hw.AcceleratorMetal,
				Memory:      hw.Memory{TotalBytes: 8 * gib},
			}, nil
		},
		ResolveHome: func() (home.Layout, error) {
			return home.Resolve(func(string) string { return tempHome }, nil)
		},
		FreeBytes: func(string) (uint64, error) {
			return 50 * gib, nil
		},
		CheckBackend: func(context.Context) (bool, []string, error) {
			return true, nil, nil
		},
		ResolveAdapter: func() (backend.Adapter, error) {
			return adapter, nil
		},
	}

	cmd := newSetupCmd(deps)
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	cmd.SetArgs([]string{"--yes"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error on pull failure")
	}
	var exit *ExitCodeError
	if !errors.As(err, &exit) || exit.Code != 2 {
		t.Errorf("expected exit code 2, got %v", err)
	}
}
