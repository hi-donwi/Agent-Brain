# ADR-0009: Gateway architecture, loopback security, role routing, and context guard

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono, Antigravity

## Context

Agent hosts (Agent-Control, Claude Code-compatible tools, opencode, Continue, Aider, and
standard OpenAI SDKs) require an OpenAI-compatible HTTP interface to interact with local
models without needing engine-specific code (ADR-0008).

However, local AI gateways face several distinct failure modes:
1. **Security & DNS Rebinding:** A loopback server without authentication or Host-header
   filtering can be hijacked by malicious JavaScript running in a user's web browser via
   DNS rebinding, allowing arbitrary code or file access.
2. **Brittle Model Naming:** Hardcoding model tags (e.g. `qwen2.5-coder:1.5b`) in tools
   breaks portability when switching hardware tiers (e.g. from `tiny` to `workstation`).
3. **Silent Context Truncation:** Upstream engines (such as Ollama's `/v1` endpoint)
   silently truncate prompts exceeding 4096 tokens without error, discarding system
   instructions and agent scratchpads.
4. **Token Exhaustion:** Agent loops that assemble large contexts need immediate and
   actionable errors when exceeding model limits, rather than obscure engine crashes.

## Decision

1. **Loopback Only by Default with Host Validation:**
   - `lokal serve` binds exclusively to `127.0.0.1` (default port `52140`).
   - Non-loopback binding requires an explicit `--allow-remote` flag, token authentication,
     and TLS.
   - An HTTP middleware inspects every incoming request's `Host` header. Requests whose
     Host header is not `127.0.0.1`, `localhost`, or matching the bound loopback address
     are immediately rejected with HTTP 403 Forbidden to prevent DNS rebinding attacks.

2. **Per-Process Bearer Token Authentication:**
   - On startup, `lokal serve` generates a cryptographically secure random bearer token
     (32 bytes hex) or reads an existing one, persisting it to `~/.lokal/serve.token` with
     restrictive mode `0600`.
   - All API endpoints (except `/healthz`) require `Authorization: Bearer <token>`.

3. **Role-Based Dynamic Routing:**
   - The gateway introduces semantic roles: `chat`, `code`, `plan`, `fast`, `embed`,
     `rerank`, and `vision`.
   - When a client sends a request for model `chat` or `code`, the gateway dynamically
     resolves the alias using the active hardware profile defined in `config.yaml`.
   - Direct backend model names (e.g. `qwen2.5-coder:1.5b`) pass through unchanged.
   - `GET /v1/models` lists both the virtual roles and all installed backend models.

4. **Context Budgeting and Non-Truncation Guard:**
   - The gateway translates `/v1/chat/completions` into the native backend adapter
     (`backend.Adapter.Chat`), strictly enforcing `options.num_ctx` matching the catalog's
     `MaxContext` or client configuration.
   - For embeddings, `/v1/embeddings` enforces `truncate: false`.
   - Requests exceeding context limits fail loudly with an OpenAI-shaped
     `context_length_exceeded` error (HTTP 400 Bad Request) rather than silently clipping.

## Consequences

### Positive
- Fully immune to browser-based DNS rebinding attacks.
- Standard OpenAI API compatibility enables zero-code integration with existing agent
  frameworks and SDKs.
- Clear role abstraction decouples client tools from underlying model tags.
- Eliminates silent prompt clipping and silent embedding corruption.

### Negative
- Client tools must provide the bearer token; `lokal doctor` prints copy-paste configuration
  snippets for supported tools.

### Neutral
- Multi-model concurrency and queueing build on this foundation in subsequent M2 rows.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Unauthenticated loopback HTTP | Vulnerable to local browser DNS rebinding attacks |
| Direct reverse proxy to Ollama `/v1` | Ollama `/v1` silently truncates prompts to 4096 tokens and ignores `num_ctx` |
| Heavy in-process C++ tokenizers | Violates pure Go static binary requirement (ADR-0001) |

## Follow-up

- M2: Residency manager, LRU memory evictions, request queueing, and streaming SSE responses.
- M2: Remote access documentation and testing through SSH tunnels and Tailscale overlays.
