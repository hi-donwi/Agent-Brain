# Roadmap

Agent-Local is built in nine milestones. Each one ships something usable on its own and
ends with explicit exit criteria. Milestones are sequential; work inside a milestone is
split into small commits, one concern each, with tests first for logic.

Status: **M0 done, M1 in progress.** Implemented so far: `lokal version`, the lokal home
layout, and Linux hardware detection.

| Milestone | Release | Theme | Status |
|---|---|---|---|
| [M0](#m0-foundation) | - | Foundation: repo, CI, ADRs, spikes | Done |
| [M1](#m1-setup-and-doctor) | v0.1 | Setup and doctor | In progress |
| [M2](#m2-gateway-and-multi-model) | v0.2 | Gateway, more backends, multi-model | Planned |
| [M3](#m3-local-retrieval) | v0.3 | Local retrieval (RAG) | Planned |
| [M4](#m4-skills-runtime-and-mcp-server) | v0.4 | Skills runtime and MCP server | Planned |
| [M5](#m5-code-knowledge-over-mcp) | v0.5 | Code knowledge over MCP | Planned |
| [M6](#m6-traces-and-eval-runner) | v0.6 | Traces and eval runner | Planned |
| [M7](#m7-skill-forge) | v0.7 | Skill forge (auto skills) | Planned |
| [M8](#m8-v10) | v1.0 | Distribution and stability | Planned |

lokal is the local AI platform that agents use (ADR-0008). The agent loop itself, with
code assistance, vibe coding, and multi-agent work, belongs to agent hosts: Agent-Control
for Agent-Workspace users, and existing agents such as opencode, Aider, Continue, Cline,
or Claude Code-compatible tools for everyone else.

Where each capability lives:

| Capability | In lokal | In the agent host |
|---|---|---|
| Setup, doctor, hardware profiles | M1 | - |
| Multi-model: roles, fallbacks, memory residency, queueing | M2 | Chooses roles per step |
| Context budgets | M2 token guard and token counting | Context assembly and compaction |
| RAG over files | M3 | Calls it over MCP |
| Skills (Agent Skills format) | M4 runtime, router, MCP | Loads and follows them |
| Code knowledge: repo map, symbols, project instructions | M5, read-only over MCP | Uses it while coding |
| Agent loop, tools, approvals, sessions | - | Agent-Control or an external agent |
| Code assistant and vibe coding (edits, git, test loop) | M6 measures models for it | Agent-Control or an external agent |
| Multi-agent orchestration | M2 keeps concurrent agents within memory | Agent-Control or an external agent |
| Traces and evaluation | M6 | Exports session logs |
| Auto skills: learn, evaluate, harden, promote | M7 | Uses promoted skills |

## Principles

- **The tiny profile is the bar.** Every feature must work on an 8 GB machine with a few
  GB of free disk. Larger machines get better models, not different code paths.
- **Fail loudly instead of degrading silently.** Truncated prompts, truncated embeddings,
  missing models, and full disks are errors with a fix, never quiet behaviour.
- **Orchestrate, do not reimplement.** Engines run models; lokal sets them up, routes,
  checks, retrieves, and learns (ADR-0002).
- **Local by default.** No egress, no telemetry, loopback only (ADR-0006).
- **Laptop or headless server, same tool.** lokal runs the same on a desktop and on a
  dedicated headless Linux VM that other machines reach through a tunnel. Nothing
  assumes a GUI, a browser, or an interactive terminal.
- **Small models first.** Everything lokal serves to agents (context budgets, structured
  output, retrieval, skills, code knowledge) is designed for short context windows and
  weaker tool use, and measured on the target profile, including a CPU-only VM.
- **Read-only toward your code.** lokal's MCP tools never write to a repository or run a
  command in it; agent hosts do that under their own approvals. Skills change only
  through review.
- **Evidence over claims.** A backend is supported when its contract tests pass; a skill
  is promoted when its evaluation shows it helps; retrieval quality is a number.

## M0: Foundation

Goal: a repository that builds, tests, and gates itself, with the expensive decisions
written down and the riskiest assumptions measured.

Deliverables:

- `lokal version` (`--json`), `cmd/` + `internal/` layout, cobra, Makefile.
- CI: gofmt, `go mod tidy` check, vet, race tests on Linux and macOS with the two
  supported Go releases, static cross-compile for five platforms, govulncheck, the
  Agent-Secure secret and dependency gate, one aggregate `CI OK` check.
- ADR-0001 to ADR-0006.
- Spike A: Ollama API surface (native and OpenAI-compatible). Result: silent prompt and
  embedding truncation, ignored `num_ctx` on `/v1`, thinking budget, working tools and
  structured output (ADR-0002).
- Spike B: pure-Go SQLite with FTS5 and brute-force vectors. Result: under 30 ms dense
  search at 100k x 768 on an 8 GB Apple M3; 3-4 s cold load (ADR-0003).

Exit criteria: CI green on `main`; ADRs accepted; spike results recorded.

## M1: Setup and doctor

Goal: from a fresh machine to a working local chat and embedding model with one command,
in under ten minutes on a normal connection, and a doctor that explains every failure.

Deliverables:

- `internal/hw`: CPU model and cores, total and available memory, Apple Silicon unified
  memory, NVIDIA (`nvidia-smi`) and AMD (`rocm-smi`) GPUs with VRAM, free disk on the
  model directory. Linux, including virtual machines (hypervisor, vCPU count, GPU
  passthrough or none), and macOS.
- `internal/backend` interface and the Ollama adapter on the native API: detect binary
  and server, version, list models with capabilities and context length (`/api/show`),
  pull with progress, chat, embed with `truncate:false`, keep-alive and unload.
- `catalog/models.yaml` (embedded): per model the roles (chat, code, embed, rerank,
  vision), parameter count, quantisation, download size, memory at the configured
  context, maximum context, licence, capabilities (tools, thinking, vision), and the
  per-backend identifier. Candidates are chosen by measurement on each profile, not
  by reputation.
- Profiles `tiny`, `standard`, `workstation`, with a default alias set each.
- `lokal setup [--profile] [--dry-run] [--yes]`: show the plan (backend to install,
  models to pull, sizes, disk after install) before doing anything; refuse a plan that
  would leave less than a safety margin of free disk; install the backend through the
  platform's package manager or print the official instruction when that needs
  privileges; pull models; write `config.yaml`; finish with the doctor. `--yes` runs
  unattended over SSH without a terminal.
- `lokal doctor [--json]`: hardware summary, backend reachable and version, every alias
  resolves to a present model, configured context fits in memory, free disk, and a smoke
  test (one chat round trip, one embedding) with tokens per second. Each failed check
  prints the command that fixes it. Exit codes: 0 healthy, 1 warnings, 2 failures.
- `lokal models ls|pull|rm`, thin wrappers that respect the catalog and disk checks.
- The M0 spikes re-run on a CPU-only Linux VM, so catalog defaults for hosts without an
  accelerator come from measured numbers, not from the Apple Silicon results.

Tests: hardware parsing from captured command output fixtures per OS; adapter tests
against recorded Ollama responses (the M0 spike cases); setup planner as a pure function
from (hardware, installed state, catalog) to plan.

Exit criteria: on a clean headless Linux VM without GPU, `lokal setup --yes` run over
SSH followed by `lokal doctor` is green; the same on an 8 GB Apple Silicon machine;
doctor output for "backend not running", "model missing", and "disk too full" is
actionable.

## M2: Gateway and multi-model

Goal: one stable local endpoint that any OpenAI-compatible tool can use, over any
supported engine, without silent truncation, with several models working together inside
the machine's memory.

Deliverables:

- `lokal serve [--port] [--profile]`: `GET /v1/models`, `POST /v1/chat/completions`
  (streaming with usage, tools, `response_format`), `POST /v1/embeddings`, `GET /healthz`.
- Roles instead of fixed model names: `chat`, `code`, `plan`, `fast`, `embed`,
  `rerank`, `vision`, each mapped per profile to a model plus an ordered fallback list;
  backend model names still pass through unchanged.
- Residency manager: knows each model's memory at its configured context (catalog), the
  machine's budget (hardware detection), and what each backend has loaded; preloads,
  keeps alive, or evicts so a request never triggers a load that does not fit. Requests
  for a model that is not resident wait in a queue with a visible status instead of
  failing.
- Concurrency limits per backend and model, queue timeouts, and cancellation that
  propagates to the backend.
- An optional remote or cloud backend only as an explicit opt-in (ADR-0006): labelled in
  every response and trace, never used as a silent fallback.
- Token counting endpoint for a model or role, so agent hosts can budget their own
  context instead of discovering the limit by a failed request.
- Designed for concurrent load from agent hosts: several agents, or one host running
  sub-agents, calling different roles at once stay within the memory budget.
- Token budget guard: requests that exceed the configured context fail with an
  OpenAI-shaped `context_length_exceeded` error. The token-count strategy per backend
  (tokenize endpoint or bundled tokenizer) is decided and recorded at the start of M2.
- Security per ADR-0006: loopback bind, per-process bearer token in a 0600 file,
  Host header check against DNS rebinding.
- Adapters for llama.cpp `llama-server`, LM Studio, MLX-LM server, and vLLM, each
  passing the shared contract test suite against recorded fixtures.
- Client snippets in `lokal doctor` for Agent-Control, Claude Code-compatible tools,
  opencode, Continue, and generic OpenAI SDKs.
- `lokal service install|uninstall|status`: a systemd unit on Linux (launchd on macOS)
  so the gateway and its backend survive reboots on a dedicated VM, logging to the
  journal.
- Remote access to a dedicated VM without leaving loopback: documented and tested
  through an SSH tunnel and through a WireGuard overlay (for example Tailscale); MCP
  over SSH stdio (`ssh <vm> lokal mcp`) so it needs no open port. A non-loopback bind
  stays an explicit opt-in that requires the token and TLS; the details are settled in
  an ADR at the start of M2.

Tests: contract suite per adapter; gateway tests with an in-process fake backend
(streaming, tool calls, errors, cancellation); residency and queue tests against fake
memory budgets; auth and Host header tests.

Exit criteria: Agent-Control, running on another machine, uses `lokal serve` on a
headless VM through a tunnel as a provider for chat and tool calls; two roles on
different models serve interleaved requests on the tiny profile without a load that
exceeds memory; every listed backend passes the contract suite.

## M3: Local retrieval

Goal: ask questions about your own files and get answers with citations, measured
against a golden set.

Deliverables:

- `internal/store`: schema and migrations for collections, documents, chunks, FTS5,
  vectors; each collection pinned to one embedding model and dimension.
- `lokal index <path> [--collection]`: ignore rules, binary and size filters, content
  hashing for incremental updates, deletion of removed files.
- Chunkers: Markdown by heading, code by line windows with overlap, plain text by
  paragraph; every chunk within the embedder's context.
- Hybrid retrieval: FTS5 bm25 + dense scan, reciprocal rank fusion, optional reranker
  when the backend provides one.
- `lokal search "<query>" [--json]`, `lokal ask "<question>"` with `path:line`
  citations and an explicit "not found in your files" answer.
- `lokal collections ls|rm|reindex`.
- Decisions recorded as ADRs: cold-start path for large collections (memory-mapped
  sidecar vs resident only); int8 vectors on or off by measured recall.
- `evals/retrieval`: a golden set over a public corpus (this repository's docs plus
  another public documentation set) with expected sources; metrics recall@5 and MRR.
  `make eval` runs it locally; CI runs the pipeline with a deterministic fake embedder.

Exit criteria: baseline recall@5 and MRR recorded for the tiny profile's embedding
model; incremental re-index touches only changed files; FTS5 and vector latency
re-measured on real text, on both a CPU-only Linux VM and Apple Silicon.

## M4: Skills runtime and MCP server

Goal: skills written for any agent work in lokal, and other agents can use lokal's
knowledge and skills over MCP.

Deliverables:

- `internal/skills`: SKILL.md parser and validator per the Agent Skills spec; sources in
  precedence order (project `.agents/skills/`, user skills, configured directories).
- Router: embedding similarity over descriptions plus keyword match; name and
  description within a fixed token budget; body and `references/` loaded on demand
  (ADR-0004).
- `lokal skills ls|show|validate|add <path>`.
- `lokal mcp` over stdio with tools `search_knowledge`, `list_skills`, `get_skill`,
  `list_models`, built on the official Go MCP SDK (version pinned and verified at the
  start of M4). Every lokal MCP tool is read-only (ADR-0008).

Tests: parser and validator against the Agent-Skills catalog; router fixtures (direct,
paraphrase, near-miss negative); MCP protocol tests through the SDK's in-memory transport.

Exit criteria: the Agent-Skills catalog loads and validates; an MCP client (Claude Code)
can search an indexed collection and fetch a skill through `lokal mcp`.

## M5: Code knowledge over MCP

Goal: any agent working on a repository (Agent-Control, Claude Code, opencode, and
others) can ask lokal what the code contains and how the project wants to be worked on,
without lokal ever touching the repository (ADR-0008).

Deliverables:

- Repository map: files with ignore rules and a symbol outline per file (Go through
  `go/ast`; other languages through a pure-Go symbol extractor, since tree-sitter needs
  cgo under ADR-0001), ranked to fit a token budget the caller passes.
- Symbol and code search: definitions and references by name, regular-expression search,
  and retrieval over the repository (M3) in one tool.
- Project instruction files (`AGENTS.md`, `CLAUDE.md`, and similar) and the project's
  `.agents/skills/` exposed as MCP resources, so agent hosts load them the same way.
- Build, lint, and test command detection (Makefile, `go.mod`, `package.json`,
  `pyproject.toml`, `Cargo.toml`, and others) returned as data for the agent host to run
  under its own approvals.
- New `lokal mcp` tools: `repo_map`, `find_symbol`, `search_code`, `project_commands`;
  all read-only.

Tests: repo-map and symbol extraction against fixture repositories in several languages;
budget tests (the map never exceeds the requested tokens); MCP protocol tests; a test that
fails if any MCP tool opens a file for writing.

Exit criteria: Claude Code, and Agent-Control once it has an MCP client, use `repo_map`,
`find_symbol`, and `search_code` on a real repository through `lokal mcp`; the read-only
test passes.

## M6: Traces and eval runner

Goal: lokal can see how agents use local models, measure which models and skills work on
this hardware, and collect the material the forge learns from.

Deliverables:

- Trace format: versioned, documented JSONL (task, model calls, tool calls and results
  truncated, outcome, timings, and the origin of every piece of content: user file,
  retrieval, tool output, MCP), with secrets redacted before writing (ADR-0006).
- Gateway capture (opt-in per client or per role): requests and responses through
  `lokal serve` written as traces, grouped into conversations.
- Importers for agent-host session logs, Agent-Control first (its replayable session
  event log), so the forge sees full sessions, not only model traffic.
- `lokal eval`: a minimal bounded runner (step, token, and time limits; native tool calls
  with a JSON-schema fallback) that executes task sets in throwaway directories only. It
  exists to measure, not to be a product.
- Task sets per model and profile: agentic tool use (read, search, structured output),
  coding tasks in temporary repositories (fix a failing test, add a function with tests,
  small refactor), and skill fixtures for the forge. Results tell agent hosts which local
  models are worth using on this machine.
- `lokal traces ls|show|import`.

Tests: trace schema round-trip and versioning; redaction tests that fail on a planted fake
secret; capture tests through the gateway with a fake backend; importer tests on recorded
Agent-Control session logs; eval-runner tests with a scripted fake backend, including a
test that fails if the runner writes outside its throwaway directory.

Exit criteria: on the target profile, the task sets produce a recorded pass rate per
model; traces from gateway capture and from an imported Agent-Control session validate
against the format and carry content origin.

## M7: Skill forge

Goal: lokal proposes new skills from your own repeated, successful work, proves they
help, and lets you promote them (ADR-0005).

Deliverables:

- `lokal forge mine`: cluster successful traces (gateway capture, imported agent-host
  sessions, and eval runs) by task embedding and tool-sequence signature; a cluster needs
  a minimum number of successful runs to qualify.
- `lokal forge draft <cluster>`: the model writes a SKILL.md candidate with provenance
  (trace IDs, content origins) into `candidates/`. Coding sessions yield project-specific
  skills (how to build and test this repository) as well as general ones.
- `lokal forge eval <candidate>`: fixtures for a direct request, a paraphrase, a near-miss
  that must not trigger, and a failure or adversarial case; run with and without the
  skill on the same fixtures through `lokal eval`; report task success, steps, tokens, and
  trigger precision and recall. Unrun cases count as failures.
- `lokal forge harden <candidate>`: bounded refinement rounds (description for routing,
  body for procedure), each re-evaluated on the same fixtures.
- Checks before promotion: structural validation, secret and personal-data scan,
  provenance flag for any external content.
- `lokal skills promote|rollback|export`: promotion shows the skill, provenance, and
  evaluation; versions by content hash; export produces a folder ready to propose to
  Agent-Skills, from which every agent host can load it.
- Decay report: promoted skills that stopped triggering or whose fixtures now fail.

Prior art: `ws learn` in Agent-Workspace drafts a skill from recorded run decisions; the
forge generalises this to agent traces and adds evaluation.

Exit criteria: from a seeded set of traces, at least one candidate is drafted, evaluated,
hardened, and promoted, with a measured improvement over the no-skill baseline and no
near-miss misfires; a candidate built from a trace with planted injected instructions is
flagged and cannot be promoted without the flag being shown.

## M8: v1.0

Goal: easy to install, stable to depend on.

Deliverables:

- Release workflow: binaries for five platforms, checksums, build provenance
  attestation, SBOM; signed tags.
- `install.sh` that verifies checksums; Homebrew tap.
- `THIRD_PARTY_LICENSES.md` generated from the module graph and checked in CI.
- Stable surfaces declared and documented: CLI commands and flags, `--json` output
  shapes, `config.yaml` schema version, gateway endpoints (including token counting), MCP
  tool names, and the trace format.
- Linux GPU path verified (NVIDIA with vLLM or llama-server); Windows support decision
  recorded as an ADR.
- Documentation per command, troubleshooting by doctor check ID, and a guide per agent
  host (Agent-Control, Claude Code, opencode, Continue, Aider).

Exit criteria: a user installs with one command on macOS and Linux, `lokal setup` and
`lokal doctor` are green on all three profiles, and the stable surfaces have tests that
pin them.

## Not planned for v1

- Running models in-process or writing inference kernels.
- An agent product of its own: an interactive agent loop, a coding agent, vibe coding, or
  multi-agent orchestration. Agent hosts own those (ADR-0008); `lokal eval` is a
  measuring tool only.
- Writing to a user's repository or running commands in it.
- A web UI (Agent-Control is the web console).
- Fine-tuning or LoRA training.
- Multi-user or networked server deployments.
- Native Windows before the M8 decision.

## Candidates after v1

Recorded so they are not lost; each needs its own evaluation before it is planned.

- Moving an agent loop into lokal, only if a revisit trigger in ADR-0008 fires.
- Vision models served for agent hosts that check screenshots of running apps.
- Shared team memory and skills across several lokal hosts.
- Voice input and output.
