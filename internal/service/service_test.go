package service_test

import (
	"os"
	"strings"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/service"
)

func TestService_UnitPath(t *testing.T) {
	tempHome := t.TempDir()

	// 1. Linux
	sType, path, err := service.UnitPath(service.Options{
		GOOS:     "linux",
		UserHome: tempHome,
	})
	if err != nil {
		t.Fatalf("UnitPath linux failed: %v", err)
	}
	if sType != "systemd" || !strings.Contains(path, ".config/systemd/user/lokal.service") {
		t.Errorf("unexpected linux path: %s %s", sType, path)
	}

	// 2. Darwin
	sType, path, err = service.UnitPath(service.Options{
		GOOS:     "darwin",
		UserHome: tempHome,
	})
	if err != nil {
		t.Fatalf("UnitPath darwin failed: %v", err)
	}
	if sType != "launchd" || !strings.Contains(path, "Library/LaunchAgents/com.github.hi-donwi.lokal.plist") {
		t.Errorf("unexpected darwin path: %s %s", sType, path)
	}

	// 3. Unsupported
	_, _, err = service.UnitPath(service.Options{
		GOOS: "plan9",
	})
	if err == nil {
		t.Errorf("expected error for plan9, got nil")
	}
}

func TestService_GenerateContent(t *testing.T) {
	tempHome := t.TempDir()

	// Linux unit content
	contentLinux, err := service.GenerateUnitContent(service.Options{
		GOOS:     "linux",
		ExecPath: "/usr/local/bin/lokal",
		UserHome: tempHome,
	})
	if err != nil {
		t.Fatalf("generate linux content failed: %v", err)
	}
	if !strings.Contains(contentLinux, "ExecStart=/usr/local/bin/lokal serve") {
		t.Errorf("missing ExecStart in linux unit:\n%s", contentLinux)
	}

	// Darwin plist content
	contentDarwin, err := service.GenerateUnitContent(service.Options{
		GOOS:     "darwin",
		ExecPath: "/opt/homebrew/bin/lokal",
		UserHome: tempHome,
	})
	if err != nil {
		t.Fatalf("generate darwin content failed: %v", err)
	}
	if !strings.Contains(contentDarwin, "<string>/opt/homebrew/bin/lokal</string>") {
		t.Errorf("missing executable in darwin plist:\n%s", contentDarwin)
	}
	if !strings.Contains(contentDarwin, "<string>serve</string>") {
		t.Errorf("missing serve argument in darwin plist:\n%s", contentDarwin)
	}
}

func TestService_InstallAndUninstall(t *testing.T) {
	tempHome := t.TempDir()
	var executedCommands [][]string

	mockRunner := func(name string, args ...string) error {
		cmd := append([]string{name}, args...)
		executedCommands = append(executedCommands, cmd)
		return nil
	}

	opts := service.Options{
		GOOS:       "linux",
		ExecPath:   "/bin/lokal",
		UserHome:   tempHome,
		CommandRun: mockRunner,
	}

	// 1. Install
	res, err := service.Install(opts)
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if res.ServiceType != "systemd" {
		t.Errorf("expected systemd service type, got %s", res.ServiceType)
	}

	// Verify file was written
	if _, err := os.Stat(res.Path); err != nil {
		t.Errorf("expected unit file to exist at %s: %v", res.Path, err)
	}

	// Verify commands run: daemon-reload, enable --now
	if len(executedCommands) < 2 {
		t.Errorf("expected at least 2 systemctl commands, got %v", executedCommands)
	}

	// 2. Status
	stat, err := service.Status(opts)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if !stat.Installed {
		t.Errorf("expected installed = true in status")
	}

	// 3. Uninstall
	unres, err := service.Uninstall(opts)
	if err != nil {
		t.Fatalf("Uninstall failed: %v", err)
	}
	if !strings.Contains(unres.Message, "removed") {
		t.Errorf("unexpected uninstall message: %s", unres.Message)
	}

	// Verify file was deleted
	if _, err := os.Stat(res.Path); !os.IsNotExist(err) {
		t.Errorf("expected unit file to be deleted, got err: %v", err)
	}
}
