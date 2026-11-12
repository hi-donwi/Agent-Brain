package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

const managementTokenPrefix = "bma1."

// NewManagementToken derives a domain-separated management credential from the
// gateway master. Rotating the master revokes all derived management credentials.
func NewManagementToken(master string) string {
	if master == "" {
		return ""
	}
	mac := hmac.New(sha256.New, []byte(master))
	_, _ = mac.Write([]byte("agent-brain-management-v1"))
	return managementTokenPrefix + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func VerifyManagementToken(master, token string) bool {
	if master == "" || len(token) > 128 || !strings.HasPrefix(token, managementTokenPrefix) {
		return false
	}
	expected := NewManagementToken(master)
	return hmac.Equal([]byte(expected), []byte(token))
}
