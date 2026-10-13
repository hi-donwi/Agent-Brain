# Architecture

This page describes the target design for v1. What exists today is listed in the
[roadmap](../ROADMAP.md); decisions behind the design are in [docs/adr](adr/README.md).

## One binary, one runtime

```
                  +------------------------------- lokal --------------------------------+
  terminal  ----> |  setup / doctor   serve (gateway)   index / ask   run   code   forge  |
  (also SSH)      |                                                                      |
  JSON events --> |  agent runtime: loop, policy, context engine, sessions, memory        |
  MCP clients --> |  orchestrator (multi-agent)          mcp (stdio)                     |
                  |                                                                      |
                  |  skills runtime     retrieval store            traces                |
                  |  (SKILL.md, router) (SQLite: FTS5 + vectors)   (JSONL + index)        |
                  |  roles + residency manager (multi-model)                             |
                  +-----------------------------------+----------------------------------+
                                                      |  HTTP, loopback
                    +--------------+------------------+---------+-------------+
                    |              |                  |         |             |
                 Ollama      llama-server         LM Studio   MLX-LM        vLLM
```

| Job | Command | What it does | Milestone |
|---|---|---|---|
| Set up | `lokal setup`, `lokal doctor`, `lokal models` | Detect hardware and backends, pick a profile, install and pull, verify end to end | M1 |
| Serve | `lokal serve`, `lokal service` | One OpenAI-compatible endpoint with roles over several models and backends, within memory | M2 |
| Know | `lokal index`, `lokal ask`, `lokal search` | Index local files and code; answer with citations | M3 |
| Share | `lokal skills`, `lokal mcp` | Load Agent Skills; expose knowledge and skills to other agents over MCP | M4 |
| Act | `lokal run`, `lokal sessions` | Bounded tool-using agent with approvals, context engine, resumable sessions, memory | M5 |
| Code | `lokal code`, `lokal code new` | Coding agent: repo map, validated edits, git checkpoints and undo, verify loop, scaffold and preview | M6 |
| Learn | `lokal forge`, `lokal skills promote` | Turn repeated successful work into tested skill candidates; promote by hand | M7 |
| Orchestrate | `lokal agents`, `lokal run --agents` | Lead agent with role workers in isolated contexts and worktrees, scheduled within memory | M8 |

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
| `internal/gateway` | OpenAI-compatible server, token guard, auth | M2 |
| `internal/models` | Roles, routing and fallbacks, residency manager, request queue | M2 |
| `internal/store` | SQLite schema, migrations, vector slab | M3 |
| `internal/rag` | Ingest, chunk, embed, hybrid retrieve, answer with citations | M3 |
| `internal/skills` | SKILL.md parse and validate, sources, router | M4 |
| `internal/mcp` | MCP server (stdio first) and MCP client | M4-M5 |
| `internal/agent` | Tool loop, tools, policy and approvals, traces | M5 |
| `internal/contextengine` | Budgeted context assembly, compaction, project instruction files | M5 |
| `internal/session` | Persistent, resumable sessions; memory proposals | M5 |
| `internal/events` | Headless JSON event stream and the terminal renderer | M5 |
| `internal/code` | Repo map, edit formats and validation, git checkpoints, verify loop, scaffolding | M6 |
| `internal/forge` | Mine, draft, evaluate, harden, promote, roll back | M7 |
| `internal/orchestrator` | Agent definitions, lead and workers, scheduler, worktree isolation | M8 |
| `catalog/` | Model catalog source, embedded at build time | M1 |
| `evals/` | Retrieval golden set, coding task set, skill fixtures, multi-agent comparisons | M3, M6-M8 |

## lokal home

Everything lokal writes lives under one directory, `~/.lokal` by default, overridable with
`LOKAL_HOME`. Deleting it resets lokal completely; models stay with their backend.

| Path | Contents |
|---|---|
| `config.yaml` | Profile, backends, aliases, skill sources (versioned schema) |
| `lokal.db` | SQLite: collections, documents, chunks, FTS5, vectors, sessions, trace index, skill registry |
| `serve.token` | Gateway bearer token for the running process (mode 0600) |
| `skills/` | Promoted skills, one folder per skill, versioned by content hash |
| `candidates/` | Forge output; never read by the router |
| `traces/` | One JSONL file per agent run (a tree of files for multi-agent runs) |
| `memory.md` | User memory: facts the user accepted, plain Markdown, editable |
| `agents/` | User-level agent definitions (Markdown with frontmatter) |

Inside a project, lokal reads `.agents/skills/`, `.agents/agents/`, the project's
instruction files (`AGENTS.md`, `CLAUDE.md`, and similar), and a project memory file; it
writes there only what the user accepted.

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
residency manager keeps the set of loaded models within the memory budget, so a coding
session that alternates between `plan` and `code` does not thrash or run out of memory.

## Context engine

Every model call the agent makes is assembled, not appended:

1. Sources are gathered: system prompt, project instruction files, selected skills,
   retrieved chunks, session history, tool results, memory.
2. Each source has a priority and a cap; the engine fills the model's token budget in
   priority order and records in the trace what was trimmed or dropped.
3. When history no longer fits, older turns and long tool outputs are compacted by the
   `fast` role into a structured working state (goal, plan, decisions, files touched,
   open questions); recent turns stay verbatim.
4. Sessions persist, so a task can be resumed after a disconnect, a crash, or the next
   day. Memory changes are proposals the user accepts.

## Coding agent

`lokal code` is the agent runtime with coding tools and rules:

```
task --> repo map + retrieval --> plan (role: plan) --> edit (role: code, edit format per model)
     --> validate edit --> checkpoint commit --> build / lint / test --> feed back or finish
```

- Works on a branch or a separate worktree, commits a checkpoint per accepted step, never
  pushes; `lokal code undo` walks back.
- Edits are validated before they touch the file; malformed edits from weak models are
  rejected and retried, not half-applied.
- `lokal code new "<description>"` scaffolds from template skills and iterates until the
  app builds and its tests pass; the dev server stays on loopback and is previewed through
  the tunnel.
- External coding tools can use the same models and knowledge through `lokal serve` and
  `lokal mcp` instead of `lokal code`.

## Multi-agent

```
              lead (role: plan)
             /       |        \
      coder (code)  tester    researcher (retrieval)
      worktree A    worktree B   read-only
             \       |        /
              reviewer gate --> merge into the task branch
```

- Agents are files (Markdown with YAML frontmatter): role, model, tools, skills, budgets.
- Workers start with fresh, narrow contexts and return typed results to the lead; there
  is no free-form agent-to-agent conversation.
- The scheduler runs workers in parallel only when their models fit in memory together.
- Multi-agent is used by default only where the eval shows it beats a single agent.

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
- Learned skills cannot activate themselves (ADR-0005); memory changes are proposals.
- Coding work happens on a branch or worktree with checkpoint commits; lokal never pushes.
- Multi-agent workers get only the tools their definition grants, and coding workers are
  isolated in their own worktrees.

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
