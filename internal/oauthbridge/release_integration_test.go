//go:build bridgeintegration

package oauthbridge

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

// Run with BRAIN_BRIDGE_TEST_ARCHIVE pointing to the official pinned release archive.
// This exercises the real installer/server without enrolling or accessing any account.
func TestPinnedReleaseStartsWithoutCredentials(t *testing.T) {
	archive := os.Getenv("BRAIN_BRIDGE_TEST_ARCHIVE")
	if archive == "" {
		t.Fatal("set BRAIN_BRIDGE_TEST_ARCHIVE to the pinned official archive")
	}
	root := t.TempDir()
	asset := artifacts[runtime.GOOS+"/"+runtime.GOARCH]
	cache := filepath.Join(root, "tools", "cliproxyapi", Version, runtime.GOOS+"-"+runtime.GOARCH)
	if err := os.MkdirAll(cache, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(cache, asset.name), data, 0600); err != nil {
		t.Fatal(err)
	}
	service := router.AccountService{Path: filepath.Join(root, "accounts.yaml")}
	a, err := service.Add(context.Background(), router.Account{Name: "work", Provider: router.ProviderAntigravity, Auth: router.AuthOAuth, ManagedBridge: true})
	if err != nil {
		t.Fatal(err)
	}
	stop, err := (Manager{Root: root, Output: io.Discard}).Start(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	if _, err = (router.BridgeClient{URL: a.BridgeURL, Key: a.BridgeKey}).Verify(context.Background(), a.Provider, ""); err == nil {
		t.Fatal("empty bridge reported a ready account")
	}
}
