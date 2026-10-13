# Architecture

This page describes the target design for v1. What exists today is listed in the
[roadmap](../ROADMAP.md); decisions behind the design are in [docs/adr](adr/README.md).

## One binary, five jobs

```
                  +----------------------------- lokal ------------------------------+
  terminal  ----> |  setup / doctor   serve (gateway)   index / ask   run   forge     |
  MCP clients --> |  mcp (stdio)                                                      |
                  |                                                                   |
                  |  skills runtime        retrieval store         traces             |
                  |  (SKILL.md, router)    (SQLite: FTS5 + vectors) (JSONL + index)   |
                  +---------------------------------+---------------------------------+
                                                    |  HTTP, loopback
                    +--------------+----------------+-----------+-------------+
                    |              |                |           |             |
                 Ollama      llama-server       LM Studio     MLX-LM        vLLM
```

| Job | Command | What it does |
|---|---|---|
| Set up | `lokal setup`, `lokal doctor` | Detect hardware and backends, pick a profile, install and pull, verify end to end |
| Serve | `lokal serve` | One OpenAI-compatible endpoint with stable aliases (`chat`, `code`, `embed`) over any backend |
| Know | `lokal index`, `lokal ask`, `lokal search` | Index local files; answer with citations from them |
| Act | `lokal run` | A small tool-using agent with approvals, guided by skills |
| Learn | `lokal forge`, `lokal skills` | Turn repeated successful work into tested skill candidates; promote by hand |

`lokal mcp` exposes knowledge search and skills to other agents (Claude Code, opencode,
Cursor, Agent-Control), so a cloud model can still use local knowledge and skills.

## Deployment shapes

lokal supports two shapes with the same binary and the same defaults.

| Shape | Where lokal and the backend run | How clients reach it |
|---|---|---|
| Workstation | The machine the user works on | Directly on `127.0.0.1` |
| Dedicated VM | A headless Linux VM (CPU-only or with GPU passthrough), run as a service | From other machines through an SSH tunnel or a WireGuard overlay (for example Tailscale); MCP as `ssh <vm> lokal mcp` over stdio |

```
  laptop                                         dedicated VM (headless Linux)
  +------------------------+   SSH tunnel or     +-------------------------------+
  | Agent-Control, editor, |   WireGuard overlay | lokal serve  (127.0.0.1)      |
  | Claude Code, scripts   | ------------------> | lokal mcp    (stdio over SSH) |
  +------------------------+                     | Ollama / llama-server / vLLM  |
                                                 | lokal home: index, traces     |
                                                 +-------------------------------+
```

On a VM the gateway still binds loopback (ADR-0006); the tunnel provides reachability
and the SSH or overlay identity provides access control on top of the bearer token. The
files to index live on the VM (synced or mounted there), so retrieval never ships file
contents across the network except as answers to the client that asked.

## Package layout

| Package | Responsibility | Milestone |
|---|---|---|
| `cmd/lokal` | Entry point | M0 |
| `internal/cli` | Command tree, flags, output formatting (human and `--json`) | M0+ |
| `internal/buildinfo` | Version, commit, build date | M0 |
| `internal/home` | lokal home layout, config load and schema version | M1 |
| `internal/hw` | CPU, memory, GPU/VRAM, accelerator, free disk detection | M1 |
| `internal/catalog` | Model catalog and hardware profiles (embedded YAML) | M1 |
| `internal/backend` | Adapter interface and one adapter per engine | M1-M2 |
| `internal/gateway` | OpenAI-compatible server, aliases, token guard, auth | M2 |
| `internal/store` | SQLite schema, migrations, vector slab | M3 |
| `internal/rag` | Ingest, chunk, embed, hybrid retrieve, answer with citations | M3 |
| `internal/skills` | SKILL.md parse and validate, sources, router | M4 |
| `internal/mcp` | MCP server (stdio first) | M4 |
| `internal/agent` | Tool loop, tools, policy and approvals, traces | M5 |
| `internal/forge` | Mine, draft, evaluate, harden, promote, roll back | M6 |
| `catalog/` | Model catalog source, embedded at build time | M1 |
| `evals/` | Retrieval golden set, skill fixtures | M3, M6 |

## lokal home

Everything lokal writes lives under one directory, `~/.lokal` by default, overridable with
`LOKAL_HOME`. Deleting it resets lokal completely; models stay with their backend.

| Path | Contents |
|---|---|
| `config.yaml` | Profile, backends, aliases, skill sources (versioned schema) |
| `lokal.db` | SQLite: collections, documents, chunks, FTS5, vectors, trace index, skill registry |
| `serve.token` | Gateway bearer token for the running process (mode 0600) |
| `skills/` | Promoted skills, one folder per skill, versioned by content hash |
| `candidates/` | Forge output; never read by the router |
| `traces/` | One JSONL file per agent run |

## Request path through the gateway

1. Client calls `POST /v1/chat/completions` with `model: "chat"` and the bearer token.
2. The gateway resolves the alias to a backend and model from the active profile.
3. It counts prompt tokens against the model's configured context and fails with a
   `context_length_exceeded` error instead of letting the backend truncate (ADR-0002).
4. The backend adapter translates to the engine's native API where that gives correct
   control (for Ollama: context size, thinking, truncation), and streams the result back
   in OpenAI shape.

## Retrieval path

1. `lokal index <path>` walks files (respecting `.gitignore` and `.lokalignore`), skips
   binaries and oversized files, hashes content, and re-processes only changed files.
2. Chunks follow structure: Markdown by heading, code by line windows with overlap (by
   declaration later), plain text by paragraph, each within the embedder's context.
   Embedding requests disable silent truncation; an overflow is a chunking bug.
3. A query runs FTS5 bm25 and a dense scan in parallel, fuses with reciprocal rank
   fusion, optionally reranks, and returns chunks with `path:line` citations.
4. `lokal ask` answers only from retrieved chunks and says so when nothing relevant is
   found. Retrieved text is data: it is quoted, never executed as instructions.

## Skill path

1. Sources are scanned (project `.agents/skills/`, user skills, configured directories).
2. Only name and description enter the prompt, within a token budget.
3. The router selects skills for the task; their bodies are loaded; `references/` files
   load on request (ADR-0004).

## Learning path (skill forge)

```
traces --> mine (repeated, successful) --> draft candidate --> evaluate (with/without)
       --> harden (bounded rounds, same fixtures) --> human promote --> skills/
```

Candidates carry provenance and an evaluation report and are invisible to the router
until promoted (ADR-0005).

## Security model

- Loopback only, bearer token, stdio MCP, no telemetry, no egress beyond configured
  endpoints (ADR-0006).
- Agent tools are classed read, write, exec, or network. Write and exec ask for approval
  unless allowed by an explicit policy; paths are confined to the working directory.
- Retrieved content, tool output, and traces are data, never instructions.
- Secrets matching known credential patterns are redacted before traces are written.
- Learned skills cannot activate themselves (ADR-0005).

## Hardware profiles

| Profile | Typical machine | Intent |
|---|---|---|
| `tiny` | 8 GB unified memory or RAM, no discrete GPU | Small chat and embedding models, short contexts, everything still works |
| `standard` | 16-32 GB unified memory, or a GPU with 8-16 GB | Mid-size chat and code models, longer contexts, reranking |
| `workstation` | 64 GB+ unified memory, or a GPU with 24 GB+ | Large or mixture-of-experts models, long contexts, parallel requests |

Profiles are data in the model catalog; each entry states memory needs *including* the
context it is configured for, because context memory is not negligible (ADR-0002).

A profile is chosen from memory *and* accelerator together. A CPU-only VM with 16-32 GB
of RAM can hold a mid-size model but generates far more slowly than Apple Silicon or a
GPU, so CPU-only hosts get smaller default models than their memory alone allows; the
defaults come from measurements on a CPU-only VM (M1).
