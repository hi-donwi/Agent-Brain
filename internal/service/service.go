package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Service identifier constants.
const (
	LaunchdLabel = "com.github.hi-donwi.lokal"
	SystemdUnit  = "lokal.service"
	DefaultPort  = 52140
	HealthURL    = "http://127.0.0.1:52140/healthz"
)

// Options specifies target environment and binary paths for service lifecycle.
type Options struct {
	GOOS       string
	ExecPath   string
	UserHome   string
	Args       []string
	CommandRun func(name string, args ...string) error
}

// Result describes the outcome of service operations.
type Result struct {
	ServiceType string `json:"service_type"`
	Path        string `json:"path"`
	Message     string `json:"message"`
}

// StatusResult describes current registration and runtime health of lokal serve.
type StatusResult struct {
	Installed   bool   `json:"installed"`
	Running     bool   `json:"running"`
	ServiceType string `json:"service_type"`
	Path        string `json:"path"`
	Version     string `json:"version,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Message     string `json:"message"`
}

func (o Options) withDefaults() (Options, error) {
	if o.GOOS == "" {
		o.GOOS = runtime.GOOS
	}
	if o.ExecPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return o, fmt.Errorf("resolve current executable: %w", err)
		}
		o.ExecPath = exe
	}
	if o.UserHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return o, fmt.Errorf("resolve user home directory: %w", err)
		}
		o.UserHome = home
	}
	if len(o.Args) == 0 {
		o.Args = []string{"serve"}
	}
	if o.CommandRun == nil {
		o.CommandRun = func(name string, args ...string) error {
			cmd := exec.Command(name, args...)
			return cmd.Run()
		}
	}
	return o, nil
}

// UnitPath returns the target unit/plist path for the active platform.
func UnitPath(opts Options) (string, string, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return "", "", err
	}

	switch opts.GOOS {
	case "linux":
		path := filepath.Join(opts.UserHome, ".config", "systemd", "user", SystemdUnit)
		return "systemd", path, nil
	case "darwin":
		path := filepath.Join(opts.UserHome, "Library", "LaunchAgents", LaunchdLabel+".plist")
		return "launchd", path, nil
	default:
		return "unsupported", "", fmt.Errorf("background service not supported on %s", opts.GOOS)
	}
}

// GenerateUnitContent generates systemd unit or launchd plist content.
func GenerateUnitContent(opts Options) (string, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return "", err
	}

	switch opts.GOOS {
	case "linux":
		content := fmt.Sprintf(`[Unit]
Description=Lokal AI Gateway Service
After=network.target

[Service]
Type=simple
ExecStart=%s serve
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
`, opts.ExecPath)
		return content, nil

	case "darwin":
		logDir := filepath.Join(opts.UserHome, ".lokal", "logs")
		logFile := filepath.Join(logDir, "serve.log")
		content := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>%s</string>
    <key>ProgramArguments</key>
    <array>
        <string>%s</string>
        <string>serve</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>%s</string>
    <key>StandardErrorPath</key>
    <string>%s</string>
</dict>
</plist>
`, LaunchdLabel, opts.ExecPath, logFile, logFile)
		return content, nil

	default:
		return "", fmt.Errorf("unsupported platform: %s", opts.GOOS)
	}
}

// Install writes service configuration and registers it with the operating system init system.
func Install(opts Options) (*Result, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}

	sType, targetPath, err := UnitPath(opts)
	if err != nil {
		return nil, err
	}

	content, err := GenerateUnitContent(opts)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(targetPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create service directory %s: %w", dir, err)
	}

	// For macOS, ensure log directory exists
	if opts.GOOS == "darwin" {
		logDir := filepath.Join(opts.UserHome, ".lokal", "logs")
		_ = os.MkdirAll(logDir, 0o755)
	}

	if err := os.WriteFile(targetPath, []byte(content), 0o644); err != nil {
		return nil, fmt.Errorf("write service file %s: %w", targetPath, err)
	}

	msg := ""
	switch opts.GOOS {
	case "linux":
		_ = opts.CommandRun("systemctl", "--user", "daemon-reload")
		_ = opts.CommandRun("systemctl", "--user", "enable", "--now", SystemdUnit)
		msg = fmt.Sprintf("systemd user service installed to %s and started", targetPath)
	case "darwin":
		_ = opts.CommandRun("launchctl", "unload", targetPath)
		_ = opts.CommandRun("launchctl", "load", targetPath)
		msg = fmt.Sprintf("launchd agent installed to %s and loaded", targetPath)
	}

	return &Result{
		ServiceType: sType,
		Path:        targetPath,
		Message:     msg,
	}, nil
}

// Uninstall stops the background service and deletes its unit/plist file.
func Uninstall(opts Options) (*Result, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}

	sType, targetPath, err := UnitPath(opts)
	if err != nil {
		return nil, err
	}

	switch opts.GOOS {
	case "linux":
		_ = opts.CommandRun("systemctl", "--user", "disable", "--now", SystemdUnit)
		_ = os.Remove(targetPath)
		_ = opts.CommandRun("systemctl", "--user", "daemon-reload")
	case "darwin":
		_ = opts.CommandRun("launchctl", "unload", targetPath)
		_ = os.Remove(targetPath)
	}

	return &Result{
		ServiceType: sType,
		Path:        targetPath,
		Message:     fmt.Sprintf("service removed from %s", targetPath),
	}, nil
}

// Status checks the installed configuration file and pings the loopback gateway.
func Status(opts Options) (*StatusResult, error) {
	opts, err := opts.withDefaults()
	if err != nil {
		return nil, err
	}

	sType, targetPath, err := UnitPath(opts)
	if err != nil {
		return nil, err
	}

	installed := false
	if _, err := os.Stat(targetPath); err == nil {
		installed = true
	}

	running := false
	version := ""
	profile := ""

	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(HealthURL)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var data struct {
			Status  string `json:"status"`
			Version string `json:"version"`
			Profile string `json:"profile"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&data); err == nil && data.Status == "ok" {
			running = true
			version = data.Version
			profile = data.Profile
		}
	}

	msg := "service is not installed and gateway is not running"
	if installed && running {
		msg = fmt.Sprintf("service is active and gateway is responding on %s", HealthURL)
	} else if installed && !running {
		msg = fmt.Sprintf("service is installed at %s but gateway is not currently reachable", targetPath)
	} else if !installed && running {
		msg = fmt.Sprintf("gateway is running on %s (managed externally, not installed as %s service)", HealthURL, sType)
	}

	return &StatusResult{
		Installed:   installed,
		Running:     running,
		ServiceType: sType,
		Path:        targetPath,
		Version:     version,
		Profile:     profile,
		Message:     msg,
	}, nil
}
