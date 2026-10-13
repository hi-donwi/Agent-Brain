# ADR-0002: Orchestrate existing inference backends; never run models in-process

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono

## Context

Running models well is a solved, fast-moving problem owned by dedicated engines: Ollama,
llama.cpp (`llama-server`), LM Studio, MLX-LM on Apple Silicon, and vLLM on Linux GPUs.
What is not solved is choosing, installing, and wiring them: which engine for this
hardware, which model fits in memory with a useful context, how to point every tool at
one endpoint, how to tell whether it is working.

All of these engines speak an OpenAI-compatible HTTP API, but "compatible" hides
differences. The M0 spike against Ollama 0.30.8 found:

- `/v1/chat/completions` silently truncates a prompt longer than the loaded context
  (4096 by default, while the model supports 40960) and returns 200 with no error;
  `options.num_ctx` on `/v1` is ignored. The native `/api/chat` honours `num_ctx`.
- `/api/embed` silently truncates input to the loaded context unless `truncate:false`.
- Thinking models spend most of the output budget on reasoning unless thinking is turned
  off (`reasoning_effort:"none"` on `/v1`, `think:false` natively).
- Tool calls and JSON-schema structured output work through `/v1`, even on a 0.6B model.
- Model memory grows with context (0.6B Q4: 798 MB at 4k, 1,052 MB at 8k).

## Decision

lokal never loads model weights itself. It drives backends over HTTP through one adapter
interface per backend (`internal/backend`). Every adapter must provide: detect (binary
and running server), version, list models with capabilities and context limits, chat
(streaming and not, tools, structured output, thinking on/off), embeddings with explicit
truncation control, and a load/unload or keep-alive hint where the engine supports it.

An adapter uses whichever API surface of its engine gives correct control. For Ollama
that is the native API for chat and embeddings (context size, `think`, `truncate`), with
`/v1` only where it is equivalent. lokal counts tokens and enforces the context budget
itself; a request that would be truncated fails with an explicit error instead.

Support order: Ollama (M1), then llama.cpp `llama-server`, LM Studio, MLX-LM, and vLLM
(M2). A backend is listed as supported only after its adapter passes the shared contract
test suite against recorded fixtures from a real server.

## Consequences

### Positive
- No GPU code, no model formats, no kernels to maintain.
- Users keep the engine they already have; lokal adds setup, routing, and checks on top.
- Silent truncation, the most damaging failure for RAG and agents, is caught by lokal.

### Negative
- Behaviour differs per engine, so the adapter contract tests are a permanent cost.
- Engine upgrades can break adapters; recorded fixtures must be refreshed per release.
- Token counting needs a tokenizer per model family or the engine's tokenize endpoint
  where one exists; the approach is settled in M2.

### Neutral
- Features an engine lacks (for example a rerank endpoint) are reported as unavailable
  for that backend, not emulated.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Embed llama.cpp via cgo bindings | Violates ADR-0001; duplicates what the engines do better |
| Treat every engine as plain OpenAI-compatible | The spike shows this silently loses context on Ollama |
| Support only Ollama | Excludes MLX (fastest on Apple Silicon) and vLLM (Linux GPU serving) |

## Follow-up

- [ ] M1: Ollama adapter on the native API, with the spike's cases as fixtures
- [ ] M2: shared adapter contract suite; llama-server, LM Studio, MLX-LM, vLLM adapters
- [ ] M2: token-budget strategy per backend (tokenize endpoint or bundled tokenizer)
