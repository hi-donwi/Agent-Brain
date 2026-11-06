# Local OpenAI Compatibility

Agent-Brain is an inference gateway, not an agent host. The client owns its system
prompt, tools, approvals, and agent loop. The gateway must not force a tool call,
rewrite an answer into a model-identity response, or turn arbitrary output JSON into
an executable tool invocation.

## Target Contract

The local Ollama path targets OpenAI Chat Completions for OpenCode, Kilo/Cline,
Continue, Aider, and the OpenAI SDK. This is not full OpenAI API parity. Client
payload fixtures do not establish that a particular editor release works end to end.

| Surface | Local support target |
|---|---|
| Models | List configured aliases and installed model IDs |
| Chat messages | String/null/text parts; developer mapped to system; tool-result replay |
| Function tools | Native calls with unique IDs, JSON-string arguments, indexed SSE deltas |
| Tool selection | Default/auto and none; required/named choices rejected, not silently ignored |
| Parallel tools | Native calls supported; parallel_tool_calls=false rejected (not enforceable by this adapter) |
| Streaming | Incremental text/tools, stop/length/tool_calls, opt-in usage, explicit stream errors |
| Structured output | json_object and json_schema mapped to Ollama format; not a promise of all strict-schema semantics |
| Context | Explicit runtime limit; messages, tool schemas, tool history, and output reserve budgeted |
| Embeddings | Text or text array, without silent truncation |
| Responses API | Provider passthrough only; no local adapter yet |
| Images/audio/files | Not supported by the text-only local Chat adapter; reject rather than omit |
| Hosted tools/state | Not supported locally (web search, file search, previous_response_id, conversations) |

Unsupported semantic options must produce an actionable error instead of claiming
success with different behavior. A client requiring Responses must use a compatible
provider account or Chat Completions mode, not an undocumented translation.

## Single 16 GB GPU Baseline

The measured pilot baseline is `qwen3:14b` Q4_K_M with a 16384-token runtime context,
one active inference, thinking disabled, and at most 2048 output tokens. The model download size alone does not
prove VRAM residency: measure loaded context, GPU offload, prompt processing,
generation throughput, and multi-turn completion. Do not load a second large model
alongside it. Evaluate a smaller model only if these measurements fail the workload.

On a single T4 with 15360 MiB VRAM, Ollama 0.40.6 reported 100% GPU residency for
this baseline. A 192-token generation sample measured 21.8 tokens/s. Three short
arithmetic tool loops, a synthetic Python fix with four tests, and an OpenCode 1.19.0
read-only tool round trip passed. These are bounded smoke checks, not a coding
benchmark or evidence of frontier-model parity.

Devstral Small 2 24B at 8k reported approximately 95% GPU residency and measured
8.28 tokens/s on a 192-token sample. It passed the tiny code-fix task but failed all
three arithmetic tool loops by emitting the next call as text. It is not the default
for this tested runner combination. The two models used different runtime contexts;
throughput is workload-specific, not a general model ranking.

Do not assume that a library tag selects the correct tool parser. The upstream
Devstral issue suggests an explicit parser variant:

```sh
curl --fail http://127.0.0.1:11434/api/create -H 'Content-Type: application/json' -d '{
  "model": "devstral-small-2:tools",
  "from": "devstral-small-2:24b",
  "parser": "ministral",
  "parameters": {"num_ctx": 8192},
  "stream": false
}'
```

This variant reuses the weights; it is not a different trained model. A successful
create response is not proof the selected runner applies the parser. Verify metadata
and rerun multi-turn tests before changing production aliases. On the tested
Ollama 0.40.6 llama.cpp runner, this recipe alone did not resolve repeated tool-call
leaks. See
[the upstream parser issue](https://github.com/ollama/ollama/issues/16932) and the
[pinned parser implementation](https://github.com/ollama/ollama/blob/v0.40.6/model/parsers/ministral.go).

Large editor instruction/tool packs can exhaust context before useful work starts. Clients
must set the real context limit and compact or reduce tools. Do not advertise the
model's theoretical maximum as this deployment's usable context.

```yaml
inference:
  context_length: 16384
  max_output_tokens: 2048
  max_concurrency: 1
```

Restart `brain serve` after changing configuration. Context accounting is a byte-based
estimate, not an exact model tokenizer; leave headroom and compact early. The token
endpoint reports the runtime context cap, while generation also reserves output space.
Native tool requests do not silently fall back to a different model on an engine error.
Native Ollama requests also disable truncation and context shifting; engines honoring
these flags reject an overflow missed by the estimate instead of discarding history.

Prefer loopback plus an SSH tunnel. A bearer token over public plain HTTP is not
transport security. Restoring an OS snapshot restores its credentials; keep snapshots
private and restore the deallocation identity/permission before leaving a VM running.

Build with at least Go 1.26.9 and run `make vulncheck`; a passing functional test
does not establish a secure runtime. Preload the selected model at the configured
context before accepting editor requests. Snapshot-backed cold reads can exceed
client timeouts. Use a bounded startup warm-up and report readiness only after it
succeeds; keep the warm model resident for the session.

## Acceptance Gate

Run unit and HTTP integration tests first, then the same requests against a real
GPU: greeting without tools; tool selection; two tool-result turns and a final
answer; streamed calls; JSON output; oversized context; cancellation/error; embeddings;
and concurrent request queueing. Report quality and latency separately from protocol
correctness. One successful glob call is not agent readiness.

## Remaining Work

Before claiming broad editor readiness, add a versioned client conformance matrix
and run actual Kilo/Cline, Continue, and Aider workflows, including their default
instruction/tool packs. The OpenCode smoke test used a restricted read-only agent,
not an unrestricted build session.

For wider OpenAI compatibility, implement and test a stateless local Responses
adapter, capability-aware parameter validation and model metadata, exact token
accounting, and VRAM-aware model profiles. The current residency manager estimates
unknown models using a fixed heuristic; it is not an actual GPU memory monitor.
Do not enable parallel large-model sessions based on its estimates alone.

Keep model/runtime/parser recipes versioned and promote them only after repeated
tool and coding gates pass. Support for an API feature and a model's ability to
perform a task are separate requirements. Preserve native parser errors and never
hide malformed model output with a fabricated successful answer.

References: [Ollama native chat](https://docs.ollama.com/api/chat),
[tool calling](https://docs.ollama.com/capabilities/tool-calling),
[OpenAI subset](https://docs.ollama.com/openai).
