// Package oauthbridge manages private, account-isolated CLIProxyAPI processes.
package oauthbridge

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const Version = "8.0.29"
const releaseRoot = "https://github.com/router-for-me/CLIProxyAPI/releases/download/v" + Version + "/"
const maxArchive = 64 << 20
const maxBinary = 128 << 20

type artifact struct{ name, digest string }

// Digests are pinned from the official v8.0.29 GitHub release asset metadata.
var artifacts = map[string]artifact{
	"darwin/arm64":  {"CLIProxyAPI_8.0.29_darwin_aarch64.tar.gz", "70fcb92a7e045dcbcb52cc63854ef723c1cb77c720923faa45d6081eeece21d0"},
	"darwin/amd64":  {"CLIProxyAPI_8.0.29_darwin_amd64.tar.gz", "ad42747bc1cf247da2a23f1804f2760d1d416db014b5590f32c2d7df3f4f54ba"},
	"linux/arm64":   {"CLIProxyAPI_8.0.29_linux_aarch64_no-plugin.tar.gz", "f8f738d7dd274d29d41b6b9b94cb64d153b8c695d1bfffe551752311a5b0c0a0"},
	"linux/amd64":   {"CLIProxyAPI_8.0.29_linux_amd64_no-plugin.tar.gz", "b34fa6c6da289936cc6515a985d733ecd943b91e287d501dc98ebc592200fd5f"},
	"windows/amd64": {"CLIProxyAPI_8.0.29_windows_amd64.zip", "f8f8de202bbe767354057baf5923b1b6959a283997ed8d695581e19e7dbf35ad"},
	"windows/arm64": {"CLIProxyAPI_8.0.29_windows_aarch64.zip", "3a957f4cdb23c0fce9d36e9960d88d36c3526583550444929a15acbd5248358d"},
}

func privateDirectory(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("bridge directory must not be a symlink")
	}
	return os.Chmod(path, 0700)
}

// EnsureBinary installs a checksum-pinned release in the runtime home, without sudo.
func EnsureBinary(ctx context.Context, root string, output io.Writer) (string, error) {
	asset, ok := artifacts[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return "", fmt.Errorf("automatic OAuth bridge is unavailable on %s/%s; configure an external bridge", runtime.GOOS, runtime.GOARCH)
	}
	dir := filepath.Join(root, "tools", "cliproxyapi", Version, runtime.GOOS+"-"+runtime.GOARCH)
	if _, err := os.Stat(filepath.Join(dir, asset.name)); os.IsNotExist(err) && output != nil {
		fmt.Fprintf(output, "Downloading CLIProxyAPI v%s for the local OAuth bridge (about 24 MB)...\n", Version)
	}
	client := &http.Client{Timeout: 3 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 || req.URL.Scheme != "https" || req.URL.User != nil {
			return fmt.Errorf("unsafe bridge download redirect")
		}
		switch req.URL.Hostname() {
		case "github.com", "release-assets.githubusercontent.com", "objects.githubusercontent.com":
			return nil
		}
		return fmt.Errorf("untrusted bridge download host")
	}}
	return install(ctx, dir, asset, client)
}

func fileDigest(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBinary {
		return "", fmt.Errorf("invalid bridge cache file")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err = io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func install(ctx context.Context, dir string, asset artifact, client *http.Client) (string, error) {
	if err := privateDirectory(dir); err != nil {
		return "", err
	}
	archive := filepath.Join(dir, asset.name)
	digest, err := fileDigest(archive)
	if err != nil || digest != asset.digest {
		if err := download(ctx, client, releaseRoot+url.PathEscape(asset.name), asset.digest, archive); err != nil {
			return "", err
		}
	}
	name := "cli-proxy-api"
	if strings.HasSuffix(asset.name, ".zip") {
		name += ".exe"
	}
	binary := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".binary-*")
	if err != nil {
		return "", err
	}
	candidate := tmp.Name()
	_ = tmp.Close()
	defer os.Remove(candidate)
	if err := extractBinary(archive, candidate, name); err != nil {
		return "", fmt.Errorf("extract OAuth bridge: %w", err)
	}
	expected, err := fileDigest(candidate)
	if err != nil {
		return "", err
	}
	if current, err := fileDigest(binary); err == nil && current == expected {
		return binary, os.Chmod(binary, 0700)
	}
	if err := os.Chmod(candidate, 0700); err != nil {
		return "", err
	}
	if err := os.Rename(candidate, binary); err != nil {
		// Another setup may have installed the identical verified executable meanwhile.
		if current, e := fileDigest(binary); e == nil && current == expected {
			return binary, nil
		}
		return "", err
	}
	return binary, nil
}

func download(ctx context.Context, client *http.Client, address, expected, destination string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Agent-Brain-OAuth-Bridge")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download OAuth bridge: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bridge download returned HTTP %d", resp.StatusCode)
	}
	f, err := os.CreateTemp(filepath.Dir(destination), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	sum := sha256.New()
	size, err := io.Copy(io.MultiWriter(f, sum), io.LimitReader(resp.Body, maxArchive+1))
	if err != nil || size > maxArchive {
		return fmt.Errorf("bridge download failed or exceeded size limit")
	}
	if hex.EncodeToString(sum.Sum(nil)) != expected {
		return fmt.Errorf("OAuth bridge checksum mismatch; nothing was executed")
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), destination)
}

func safeArchiveName(name string) bool {
	name = strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(name, "/") || strings.Contains(name, ":") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

func extractBinary(archive, destination, name string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	output, err := os.OpenFile(destination, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0600)
	if err != nil {
		return err
	}
	defer output.Close()
	found := false
	copyEntry := func(entry string, size int64, regular bool, reader io.Reader) error {
		if !safeArchiveName(entry) || !regular {
			return fmt.Errorf("unsafe archive entry")
		}
		if strings.TrimPrefix(entry, "./") != name {
			return nil
		}
		if found || size <= 0 || size > maxBinary {
			return fmt.Errorf("invalid or duplicate bridge executable")
		}
		found = true
		n, err := io.Copy(output, io.LimitReader(reader, maxBinary+1))
		if err != nil {
			return err
		}
		if n != size {
			return fmt.Errorf("invalid executable size")
		}
		return nil
	}
	if strings.HasSuffix(archive, ".zip") {
		info, err := f.Stat()
		if err != nil {
			return err
		}
		zr, err := zip.NewReader(f, info.Size())
		if err != nil {
			return err
		}
		if len(zr.File) > 256 {
			return fmt.Errorf("too many archive entries")
		}
		for _, entry := range zr.File {
			if entry.FileInfo().IsDir() {
				if !safeArchiveName(entry.Name) {
					return fmt.Errorf("unsafe archive directory")
				}
				continue
			}
			r, err := entry.Open()
			if err != nil {
				return err
			}
			err = copyEntry(entry.Name, int64(entry.UncompressedSize64), entry.Mode().IsRegular(), r)
			_ = r.Close()
			if err != nil {
				return err
			}
		}
	} else {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return err
		}
		defer gz.Close()
		tr := tar.NewReader(io.LimitReader(gz, 256<<20))
		for count := 0; ; count++ {
			header, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return err
			}
			if count >= 256 {
				return fmt.Errorf("too many archive entries")
			}
			if header.Typeflag == tar.TypeDir {
				if !safeArchiveName(header.Name) {
					return fmt.Errorf("unsafe archive directory")
				}
				continue
			}
			if err := copyEntry(header.Name, header.Size, header.Typeflag == tar.TypeReg, tr); err != nil {
				return err
			}
		}
	}
	if !found {
		return fmt.Errorf("bridge executable missing from archive")
	}
	return output.Close()
}
