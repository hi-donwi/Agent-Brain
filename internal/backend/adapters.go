package backend

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

const (
	// DefaultLlamaServerEndpoint is the standard loopback address for llama.cpp server.
	DefaultLlamaServerEndpoint = "http://127.0.0.1:8080"
	// DefaultVLLMEndpoint is the standard loopback address for vLLM API server.
	DefaultVLLMEndpoint = "http://127.0.0.1:8000"
	// DefaultLMStudioEndpoint is the standard loopback address for LM Studio local server.
	DefaultLMStudioEndpoint = "http://127.0.0.1:1234"
	// DefaultGatewayEndpoint is the standard loopback address for lokal serve.
	DefaultGatewayEndpoint = "http://127.0.0.1:52140"
)

// LlamaServerAdapter drives llama.cpp llama-server over its OpenAI and native HTTP endpoints.
type LlamaServerAdapter struct {
	*RemoteAdapter
}

// NewLlamaServer creates an adapter for llama.cpp llama-server.
func NewLlamaServer(endpoint string, client *http.Client) *LlamaServerAdapter {
	ep := strings.TrimRight(endpoint, "/")
	if ep == "" {
		ep = DefaultLlamaServerEndpoint
	}
	base := NewRemote(ep, "", client)
	base.SetName("llamacpp")
	return &LlamaServerAdapter{RemoteAdapter: base}
}

// Ping checks if llama-server is healthy using /health or /v1/models.
func (l *LlamaServerAdapter) Ping(ctx context.Context) error {
	req, err := l.newRequest(ctx, http.MethodGet, "/health", nil)
	if err == nil {
		resp, err := l.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
	return l.RemoteAdapter.Ping(ctx)
}

// Version returns engine version for llama-server.
func (l *LlamaServerAdapter) Version(_ context.Context) (string, error) {
	return "llama-server", nil
}

// VLLMAdapter drives a vLLM inference engine.
type VLLMAdapter struct {
	*RemoteAdapter
}

// NewVLLM creates an adapter for vLLM.
func NewVLLM(endpoint, token string, client *http.Client) *VLLMAdapter {
	ep := strings.TrimRight(endpoint, "/")
	if ep == "" {
		ep = DefaultVLLMEndpoint
	}
	base := NewRemote(ep, token, client)
	base.SetName("vllm")
	return &VLLMAdapter{RemoteAdapter: base}
}

// Ping checks if vLLM is healthy using /health or /v1/models.
func (v *VLLMAdapter) Ping(ctx context.Context) error {
	req, err := v.newRequest(ctx, http.MethodGet, "/health", nil)
	if err == nil {
		resp, err := v.httpClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
	}
	return v.RemoteAdapter.Ping(ctx)
}

// Version returns engine identifier for vLLM.
func (v *VLLMAdapter) Version(_ context.Context) (string, error) {
	return "vllm", nil
}

// LMStudioAdapter drives an LM Studio local server.
type LMStudioAdapter struct {
	*RemoteAdapter
}

// NewLMStudio creates an adapter for LM Studio.
func NewLMStudio(endpoint string, client *http.Client) *LMStudioAdapter {
	ep := strings.TrimRight(endpoint, "/")
	if ep == "" {
		ep = DefaultLMStudioEndpoint
	}
	base := NewRemote(ep, "", client)
	base.SetName("lmstudio")
	return &LMStudioAdapter{RemoteAdapter: base}
}

// Version returns engine identifier for LM Studio.
func (m *LMStudioAdapter) Version(_ context.Context) (string, error) {
	return "lmstudio", nil
}

// NewAdapter instantiates the appropriate Adapter based on engine name and configuration.
func NewAdapter(engine, endpoint, token string, client *http.Client) (Adapter, error) {
	engine = strings.ToLower(strings.TrimSpace(engine))
	switch engine {
	case "", "ollama":
		if endpoint == "" {
			endpoint = DefaultOllamaEndpoint
		}
		return NewOllama(endpoint, client), nil
	case "remote", "gateway":
		if endpoint == "" {
			endpoint = DefaultGatewayEndpoint
		}
		return NewRemote(endpoint, token, client), nil
	case "llamacpp", "llama-server", "llama.cpp":
		if endpoint == "" {
			endpoint = DefaultLlamaServerEndpoint
		}
		return NewLlamaServer(endpoint, client), nil
	case "vllm":
		if endpoint == "" {
			endpoint = DefaultVLLMEndpoint
		}
		return NewVLLM(endpoint, token, client), nil
	case "lmstudio", "lm-studio":
		if endpoint == "" {
			endpoint = DefaultLMStudioEndpoint
		}
		return NewLMStudio(endpoint, client), nil
	default:
		return nil, fmt.Errorf("unsupported backend engine: %q (supported: ollama, remote, llamacpp, vllm, lmstudio)", engine)
	}
}
