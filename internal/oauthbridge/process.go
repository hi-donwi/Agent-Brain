package oauthbridge

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"gopkg.in/yaml.v3"
)

type Manager struct {
	Root    string
	Output  io.Writer
	binary  func(context.Context, string, io.Writer) (string, error)
	command func(context.Context, string, ...string) *exec.Cmd
}

// Use borrows a running authenticated bridge, or owns a temporary process.
func (m Manager) Use(ctx context.Context, a router.Account) (func(), error) {
	if !a.ManagedBridge {
		return func() {}, nil
	}
	probe, cancel := context.WithTimeout(ctx, time.Second)
	err := (router.BridgeClient{URL: a.BridgeURL, Key: a.BridgeKey}).Check(probe)
	cancel()
	if err == nil {
		return func() {}, nil
	}
	return m.Start(ctx, a)
}

// Start owns a bridge for one command's lifetime. Its lock is released by the OS
// even if Brain crashes. Stop only signals the process this invocation created.
func (m Manager) Start(ctx context.Context, a router.Account) (func(), error) {
	if !a.ManagedBridge {
		return func() {}, nil
	}
	u, err := url.Parse(a.BaseURL)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Path != "" || u.RawQuery != "" || u.User != nil || u.Fragment != "" {
		return nil, fmt.Errorf("invalid managed bridge address")
	}
	if a.BridgeURL != a.BaseURL+"/v8/management" {
		return nil, fmt.Errorf("managed bridge management address does not match its inference address")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid managed bridge port")
	}
	key, err := router.ResolveCredential(a.APIKey)
	if err != nil || key == "" {
		return nil, fmt.Errorf("managed bridge inference credential is unavailable")
	}
	management, err := router.ResolveCredential(a.BridgeKey)
	if err != nil || management == "" {
		return nil, fmt.Errorf("managed bridge management credential is unavailable")
	}
	sum := sha256.Sum256([]byte(a.ID))
	dir := filepath.Join(m.Root, "bridges", fmt.Sprintf("%x", sum[:16]))
	if err := privateDirectory(dir); err != nil {
		return nil, err
	}
	lockPath := filepath.Join(dir, "process.lock")
	if info, err := os.Lstat(lockPath); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid bridge process lock")
	}
	lock, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, err
	}
	if err := lockProcessFile(lock); err != nil {
		_ = lock.Close()
		return nil, fmt.Errorf("another Brain operation owns this bridge; finish it before starting the gateway")
	}
	owned := false
	defer func() {
		if !owned {
			_ = lock.Close()
		}
	}()
	listener, err := net.Listen("tcp4", u.Host)
	if err != nil {
		return nil, fmt.Errorf("managed bridge port is already in use; stop the existing gateway before starting another")
	}
	_ = listener.Close()
	if err := privateDirectory(filepath.Join(dir, "auth")); err != nil {
		return nil, err
	}
	cfg := map[string]any{
		"config-version": 8,
		"server":         map[string]any{"host": "127.0.0.1", "port": port, "max-request-body-bytes": 16 << 20, "discovery": map[string]any{"enabled": false}},
		"management":     map[string]any{"allow-remote": false, "secret-key": management, "disable-control-panel": true},
		"access":         map[string]any{"api-keys": []string{key}},
		"oauth":          map[string]any{"auth-dir": filepath.Join(dir, "auth")},
		"api-keys":       map[string]any{}, "plugins": map[string]any{"enabled": false},
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	configPath := filepath.Join(dir, "config.yaml")
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return nil, err
	}
	if err = tmp.Close(); err != nil {
		return nil, err
	}
	if err = os.Rename(tmp.Name(), configPath); err != nil {
		return nil, err
	}
	ensure := m.binary
	if ensure == nil {
		ensure = EnsureBinary
	}
	executable, err := ensure(ctx, m.Root, m.Output)
	if err != nil {
		return nil, err
	}
	command := m.command
	if command == nil {
		command = exec.CommandContext
	}
	child := command(ctx, executable, "-config", configPath, "-local-model")
	child.Cancel = func() error {
		if child.Process == nil {
			return os.ErrProcessDone
		}
		return terminateProcess(child.Process)
	}
	child.WaitDelay = 3 * time.Second
	child.Dir = dir
	child.Env = bridgeEnvironment()
	logPath := filepath.Join(dir, "bridge.log")
	if info, err := os.Lstat(logPath); err == nil && !info.Mode().IsRegular() {
		return nil, fmt.Errorf("invalid bridge log file")
	}
	log, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return nil, err
	}
	child.Stdout, child.Stderr = log, log
	if err = child.Start(); err != nil {
		_ = log.Close()
		return nil, fmt.Errorf("start managed OAuth bridge: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			_ = terminateProcess(child.Process)
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				_ = child.Process.Kill()
				<-done
			}
			_ = log.Close()
			_ = lock.Close()
		})
	}
	owned = true
	ready, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	for {
		select {
		case err := <-done:
			stop()
			return nil, fmt.Errorf("managed bridge exited before startup (%v); inspect private bridge.log", err)
		default:
		}
		check, cancel := context.WithTimeout(ready, 500*time.Millisecond)
		err := (router.BridgeClient{URL: a.BridgeURL, Key: a.BridgeKey, Client: &http.Client{Timeout: 500 * time.Millisecond, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}).Check(check)
		cancel()
		if err == nil {
			return stop, nil
		}
		select {
		case <-ready.Done():
			stop()
			return nil, fmt.Errorf("managed OAuth bridge did not become ready; inspect private bridge.log")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func bridgeEnvironment() []string {
	allowed := map[string]bool{}
	for _, name := range []string{"PATH", "HOME", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "SYSTEMROOT", "WINDIR", "TMPDIR", "TMP", "TEMP", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "SSL_CERT_FILE", "SSL_CERT_DIR"} {
		allowed[name] = true
	}
	var result []string
	for _, entry := range os.Environ() {
		name, _, ok := strings.Cut(entry, "=")
		if ok && allowed[strings.ToUpper(name)] {
			result = append(result, entry)
		}
	}
	return result
}
