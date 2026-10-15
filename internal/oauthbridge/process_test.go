package oauthbridge

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/router"
	"gopkg.in/yaml.v3"
)

func TestManagedBridgeHelper(t *testing.T) {
	index := -1
	for i, arg := range os.Args {
		if arg == "--bridge-test-helper" {
			index = i
		}
	}
	if index < 0 {
		return
	}
	data, err := os.ReadFile(os.Args[index+1])
	if err != nil {
		os.Exit(2)
	}
	var config struct {
		Server struct {
			Host string
			Port int
		}
		Management struct {
			Key string `yaml:"secret-key"`
		}
	}
	if yaml.Unmarshal(data, &config) != nil {
		os.Exit(2)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+config.Management.Key {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"config-version":8,"api-keys":{},"plugins":{"enabled":false}}`)
	})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() { <-signals; _ = os.WriteFile(os.Args[index+1]+".stopped", []byte("stopped"), 0600); os.Exit(0) }()
	_ = http.ListenAndServe(fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port), handler)
	os.Exit(0)
}

func TestManagedProcessStopsAndDoesNotInheritProviderSecrets(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OPENAI_API_KEY", "must-not-reach-bridge")
	t.Setenv("MANAGEMENT_PASSWORD", "must-not-reach-bridge")
	s := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	a, err := s.Add(context.Background(), router.Account{Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth, ManagedBridge: true})
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var child *exec.Cmd
	m := Manager{Root: root, binary: func(context.Context, string, io.Writer) (string, error) { return executable, nil }, command: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		child = exec.CommandContext(ctx, executable, "-test.run=^TestManagedBridgeHelper$", "--", "--bridge-test-helper", args[1])
		return child
	}}
	stop, err := m.Start(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	for _, entry := range child.Env {
		if strings.HasPrefix(entry, "OPENAI_API_KEY=") || strings.HasPrefix(entry, "MANAGEMENT_PASSWORD=") {
			t.Fatal("ambient credential inherited")
		}
	}
	if _, err := m.Start(context.Background(), a); err == nil {
		t.Fatal("second process acquired an owned bridge")
	}
	reused, err := m.Use(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	reused()
	stop()
	if runtime.GOOS != "windows" {
		configPath := child.Args[len(child.Args)-1]
		if _, err := os.Stat(configPath + ".stopped"); err != nil {
			t.Fatal("bridge was killed before graceful shutdown")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := (router.BridgeClient{URL: a.BridgeURL, Key: a.BridgeKey}).Check(ctx); err == nil {
		t.Fatal("owned process survived stop")
	}
	stopAgain, err := m.Start(context.Background(), a)
	if err != nil {
		t.Fatalf("process lock was not released: %v", err)
	}
	stopAgain()
}
