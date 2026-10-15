package oauthbridge

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func testArchive(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, name := range names {
		data := []byte("fake-executable")
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: int64(len(data))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestInstallChecksDigestBeforeExtractingAndUsesVerifiedCache(t *testing.T) {
	data := testArchive(t, "cli-proxy-api")
	digest := sha256.Sum256(data)
	asset := artifact{name: "test.tar.gz", digest: fmt.Sprintf("%x", digest)}
	calls := 0
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data))}, nil
	})}
	root := t.TempDir()
	path, err := install(context.Background(), root, asset, client)
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(path); err != nil || string(content) != "fake-executable" {
		t.Fatalf("installed content: %v", err)
	}
	if _, err = install(context.Background(), root, asset, client); err != nil || calls != 1 {
		t.Fatalf("cache not reused: %d %v", calls, err)
	}
	asset.digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err = install(context.Background(), t.TempDir(), asset, client); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
}

func TestExtractionRejectsTraversalAndDuplicateExecutables(t *testing.T) {
	for _, names := range [][]string{{"../cli-proxy-api"}, {"cli-proxy-api", "cli-proxy-api"}} {
		root := t.TempDir()
		archive := filepath.Join(root, "asset.tar.gz")
		if err := os.WriteFile(archive, testArchive(t, names...), 0600); err != nil {
			t.Fatal(err)
		}
		if err := extractBinary(archive, filepath.Join(root, "binary"), "cli-proxy-api"); err == nil {
			t.Fatal("unsafe archive accepted")
		}
	}
}

func TestWindowsArchiveExtractsOnlyTheExpectedExecutable(t *testing.T) {
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create("cli-proxy-api.exe")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte("fake-windows-executable")); err != nil {
		t.Fatal(err)
	}
	if err = archive.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "release.zip")
	binary := filepath.Join(root, "bridge.exe")
	if err = os.WriteFile(path, buffer.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err = extractBinary(path, binary, "cli-proxy-api.exe"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil || string(data) != "fake-windows-executable" {
		t.Fatalf("windows binary: %v", err)
	}
}
