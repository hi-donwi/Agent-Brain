package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hi-donwi/Agent-Brain/internal/router"
)

const inferenceTokenVersion = "bpi1"

var profileRevisionPattern = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

// InferenceScope is the authorization carried by an Agent-Control inference credential.
type InferenceScope struct {
	ProfileID string   `json:"profile_id"`
	Revision  string   `json:"revision"`
	Protocols []string `json:"protocols"`
}

type inferenceScopeContextKey struct{}

// InferenceScopeFromContext returns the verified routing scope on a profile-authenticated request.
func InferenceScopeFromContext(ctx context.Context) (InferenceScope, bool) {
	scope, ok := ctx.Value(inferenceScopeContextKey{}).(InferenceScope)
	return scope, ok
}

// NewInferenceToken signs a single-profile, content-revision, protocol-scoped token with the gateway master.
func NewInferenceToken(master string, scope InferenceScope) (string, error) {
	if master == "" {
		return "", fmt.Errorf("gateway master token is empty")
	}
	canonical, err := canonicalInferenceScope(scope)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode inference scope: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	message := inferenceTokenVersion + "." + encoded
	mac := hmac.New(sha256.New, []byte(master))
	_, _ = mac.Write([]byte(message))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return message + "." + signature, nil
}

// VerifyInferenceToken verifies the signature and rejects non-canonical or unsupported scopes.
func VerifyInferenceToken(master, token string) (InferenceScope, bool) {
	if master == "" || len(token) > 4096 {
		return InferenceScope{}, false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != inferenceTokenVersion {
		return InferenceScope{}, false
	}
	message := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, []byte(master))
	_, _ = mac.Write([]byte(message))
	signature, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(signature, mac.Sum(nil)) {
		return InferenceScope{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || len(payload) > 2048 {
		return InferenceScope{}, false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var scope InferenceScope
	if decoder.Decode(&scope) != nil {
		return InferenceScope{}, false
	}
	canonical, err := canonicalInferenceScope(scope)
	if err != nil {
		return InferenceScope{}, false
	}
	canonicalPayload, err := json.Marshal(canonical)
	if err != nil || !bytes.Equal(payload, canonicalPayload) {
		return InferenceScope{}, false
	}
	return canonical, true
}

func canonicalInferenceScope(scope InferenceScope) (InferenceScope, error) {
	if !router.ProfileIDValid(scope.ProfileID) || !profileRevisionPattern.MatchString(scope.Revision) {
		return InferenceScope{}, fmt.Errorf("inference scope requires a valid profile ID and sha256 revision")
	}
	if len(scope.Protocols) == 0 || len(scope.Protocols) > 5 {
		return InferenceScope{}, fmt.Errorf("inference scope requires between one and five protocols")
	}
	protocols := append([]string(nil), scope.Protocols...)
	sort.Strings(protocols)
	for i, protocol := range protocols {
		if protocol != router.ProtocolChat && protocol != router.ProtocolResponses && protocol != router.ProtocolMessages && protocol != router.ProtocolGemini && protocol != router.ProtocolEmbeddings {
			return InferenceScope{}, fmt.Errorf("unsupported inference protocol %q", protocol)
		}
		if i > 0 && protocols[i-1] == protocol {
			return InferenceScope{}, fmt.Errorf("duplicate inference protocol %q", protocol)
		}
	}
	scope.Protocols = protocols
	return scope, nil
}
