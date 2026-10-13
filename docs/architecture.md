# Architecture

This page describes the target design for v1. What exists today is listed in the
[roadmap](../ROADMAP.md); decisions behind the design are in [docs/adr](adr/README.md).

## One binary, the local layer under any agent

```
  agent hosts                                 lokal (local AI platform)
  +------------------------------+            +--------------------------------------------+
  | Agent-Control (agent host +  |  OpenAI    | serve: roles, fallbacks, residency, queue, |
  | web console), Claude Code,   | ---------> |        token guard, token counting         |
  | opencode, Aider, Continue,   |            |                                            |
  | Cline, scripts               |  MCP       | mcp:   retrieval, code knowledge, skills   |
  |                              | ---------> |        (read-only)                         |
  | agent loop, tools, approvals |            |                                            |
  | coding, multi-agent, UI      |  traces    | traces, eval runner, skill forge           |
  +------------------------------+ ---------> | setup / doctor / models                    |
                                              +----------------------+---------------------+
                                                                     |  HTTP, loopback
                                    +-------------+---------------+--+--------+-------------+
                                    |             |               |           |             |
                                 Ollama     llama-server      LM Studio     MLX-LM        vLLM
```

lokal does not run an agent loop of its own (ADR-0008). Agent hosts run the loop, the
tools, the approvals, coding sessions, and multi-agent work; lokal makes local models,
knowledge, and skills good enough for them, and learns from what they do.

| Job | Command | What it does | Milestone |
|---|---|---|---|
| Set up | `lokal setup`, `lokal doctor`, `lokal models` | Detect hardware and backends, pick a profile, install and pull, verify end to end | M1 |
| Serve | `lokal serve`, `lokal service` | One OpenAI-compatible endpoint with roles over several models and backends, within memory | M2 |
| Know | `lokal index`, `lokal ask`, `lokal search` | Index local files; answer with citations | M3 |
| Share | `lokal skills`, `lokal mcp` | Load Agent Skills; expose knowledge, code knowledge, and skills to agents over MCP | M4, M5 |
| Measure | `lokal traces`, `lokal eval` | Capture and import traces; measure models and skills on this hardware | M6 |
| Learn | `lokal forge`, `lokal skills promote` | Turn repeated successful work into tested skill candidates; promote by hand | M7 |

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
| `internal/hw` | CPU, memory, GPU/VRAM, accelerator, virtualization, free disk | M1 |
| `internal/catalog` | Model catalog and hardware profiles (embedded YAML) | M1 |
| `internal/backend` | Adapter interface and one adapter per engine | M1-M2 |
| `internal/gateway` | OpenAI-compatible server, token guard, token counting, auth | M2 |
| `internal/models` | Roles, routing and fallbacks, residency manager, request queue | M2 |
| `internal/store` | SQLite schema, migrations, vector slab | M3 |
| `internal/rag` | Ingest, chunk, embed, hybrid retrieve, answer with citations | M3 |
| `internal/skills` | SKILL.md parse and validate, sources, router | M4 |
| `internal/mcp` | MCP server (stdio first); every tool read-only | M4-M5 |
| `internal/codeintel` | Repo map, symbol extraction, code search, project commands and instructions | M5 |
| `internal/trace` | Trace format, gateway capture, redaction, agent-host session importers | M6 |
| `internal/evalrun` | Bounded eval runner in throwaway directories, task sets | M6 |
| `internal/forge` | Mine, draft, evaluate, harden, promote, roll back | M7 |
| `catalog/` | Model catalog source, embedded at build time | M1 |
| `evals/` | Retrieval golden set, agentic and coding task sets, skill fixtures | M3, M6-M7 |

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
| `traces/` | Captured and imported traces, one JSONL file per conversation or session |

Inside a project, lokal only reads: `.agents/skills/`, the project's instruction files
(`AGENTS.md`, `CLAUDE.md`, and similar), and the source for code knowledge. It never
writes there.

## Request path through the gateway

1. Client calls `POST /v1/chat/completions` with `model: "chat"` and the bearer token.
2. The gateway resolves the role to a backend and model from the active profile, or to
   the first fallback that is available.
3. The residency manager admits the request: the model is loaded, or it can be loaded
   within the memory budget after evicting idle models, or the request waits in the queue
   with a visible status.
4. It counts prompt tokens against the model's configured context and fails with a
   `context_length_exceeded` error instead of letting the backend truncate (ADR-0002).
5. The backend adapter translates to the engine's native API where that gives correct
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

## Multi-model

Callers ask for a role, not a model. Each profile maps roles to models:

| Role | Used for |
|---|---|
| `chat` | General answers |
| `code` | Code generation and edits |
| `plan` | Planning and review steps that benefit from the strongest model that fits |
| `fast` | Routing, classification, summarising for compaction |
| `embed`, `rerank` | Retrieval |
| `vision` | Image input, where a vision model is installed |

On small machines several roles share one model; on larger ones they split. The
residency manager keeps the set of loaded models within the memory budget, so an agent
host's coding session that alternates between `plan` and `code`, or several sub-agents
calling at once, does not thrash or run out of memory. Agent hosts can ask the gateway
how many tokens a prompt takes for a role before they send it.

## How agent hosts use lokal

| Need of the agent host | lokal provides | Interface |
|---|---|---|
| Local models that fit and never truncate silently | Roles, residency, queue, token guard | OpenAI-compatible gateway |
| Budgeting its own context | Token counting per role | Gateway endpoint |
| Knowledge about files | Hybrid retrieval with citations | MCP `search_knowledge` |
| Knowledge about code | Repo map, symbols, code search, project commands and instructions | MCP, read-only |
| Procedures | Skills and the router | MCP `list_skills`, `get_skill` |
| Knowing which local model can do the job | Pass rates per model and profile | `lokal eval` reports |
| Getting better over time | Skills learned from its own traces | Trace import, forge, Agent-Skills export |

Agent-Control reaches a lokal gateway on a VM through an SSH tunnel, so the endpoint is a
loopback URL and counts as `local` under its egress rules; an overlay or LAN address would
count as `remote` and need a per-project allowlist entry. A split deployment also works:
`lokal mcp` and `lokal index` on the workstation where the files are, `lokal serve` on the
VM where the models are. The workstation's lokal then has no models of its own: its
backend is the VM's gateway, reached through the same tunnel.

## Learning path (skill forge)

```
gateway capture ----+
agent-host sessions -+--> traces --> mine (repeated, successful) --> draft candidate
eval runs ----------+         --> evaluate with/without (lokal eval) --> harden
                              --> human promote --> skills/ --> Agent-Skills --> every agent host
```

Candidates carry provenance and an evaluation report and are invisible to the router
until promoted (ADR-0005).

## Security model

- Loopback only, bearer token, stdio MCP, no telemetry, no egress beyond configured
  endpoints (ADR-0006).
- lokal's MCP tools are read-only; lokal never writes to a repository or runs a command
  in it. Writes and commands happen in the agent host under its approvals (ADR-0008).
- `lokal eval` runs tasks only in throwaway directories it creates.
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
