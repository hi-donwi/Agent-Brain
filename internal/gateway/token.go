package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const tokenBytes = 32

// GenerateToken generates a cryptographically secure random 64-character hex token.
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read secure random bytes: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// LoadOrCreateToken reads an existing valid token from tokenPath or generates
// and atomically persists a new one with mode 0600.
func LoadOrCreateToken(tokenPath string) (string, error) {
	if tokenPath == "" {
		return "", errors.New("token path cannot be empty")
	}

	data, err := os.ReadFile(tokenPath)
	if err == nil {
		token := strings.TrimSpace(string(data))
		if len(token) == tokenBytes*2 {
			if _, err := hex.DecodeString(token); err == nil {
				return token, nil
			}
		}
	}

	// Generate a fresh token
	token, err := GenerateToken()
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(tokenPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create directory %s: %w", dir, err)
	}

	tmpFile := filepath.Join(dir, fmt.Sprintf(".%s.tmp", filepath.Base(tokenPath)))
	if err := os.WriteFile(tmpFile, []byte(token+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write temporary token: %w", err)
	}

	if err := os.Rename(tmpFile, tokenPath); err != nil {
		_ = os.Remove(tmpFile)
		return "", fmt.Errorf("persist token to %s: %w", tokenPath, err)
	}

	return token, nil
}
