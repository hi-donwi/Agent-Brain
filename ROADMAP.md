# Roadmap

Agent-Local is built in ten milestones. Each one ships something usable on its own and
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
| [M5](#m5-agent-runtime-and-context-engine) | v0.5 | Agent runtime and context engine | Planned |
| [M6](#m6-coding-agent-and-vibe-coding) | v0.6 | Coding agent and vibe coding | Planned |
| [M7](#m7-skill-forge) | v0.7 | Skill forge (auto skills) | Planned |
| [M8](#m8-multi-agent) | v0.8 | Multi-agent | Planned |
| [M9](#m9-v10) | v1.0 | Distribution and stability | Planned |

Where each capability lives:

| Capability | Milestones |
|---|---|
| Setup, doctor, hardware profiles | M1 |
| Multi-model: roles, routing, fallbacks, memory residency | M2, used by M5 and M8 |
| RAG over files and code | M3, code-aware in M6 |
| Skills (Agent Skills format) and MCP | M4 |
| Agent loop, context engine, sessions, memory | M5 |
| Code assistant and vibe coding | M6 |
| Auto skills: learn, evaluate, harden, promote | M7 |
| Multi-agent orchestration | M8 |

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
- **Small models first.** Every agent feature is designed for short context windows
  and weaker tool use (narrow tool sets, explicit budgets, structured edits) and measured
  on the target profile, including a CPU-only VM.
- **Reversible by default.** File edits land in git checkpoints; skills and memory
  change only through review.
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
  start of M4).

Tests: parser and validator against the Agent-Skills catalog; router fixtures (direct,
paraphrase, near-miss negative); MCP protocol tests through the SDK's in-memory transport.

Exit criteria: the Agent-Skills catalog loads and validates; an MCP client (Claude Code)
can search an indexed collection and fetch a skill through `lokal mcp`.

## M5: Agent runtime and context engine

Goal: a bounded, safe agent loop that works with small local models, manages its context
explicitly, survives long sessions, and records traces good enough to learn from
(ADR-0007).

Deliverables:

- `lokal run "<task>" [--dir] [--model]`: bounded loop (step, token, and time limits),
  thinking off by default, native tool calls with a JSON-schema fallback for models
  without tool support, validation errors fed back for retry, cancellation at any step.
- Tools: `read_file`, `list_dir`, `search_files`, `search_knowledge`, `write_file` (diff
  preview), `run_shell`, and tools from configured MCP servers (MCP client). Browser,
  web, and other heavy tools come from MCP servers, not from lokal itself.
- Policy: tools classed read, write, exec, network; write and exec require approval
  unless an explicit allow rule matches; paths confined to `--dir`; no network tools by
  default.
- Context engine: every model call is assembled from ranked sources within the model's
  token budget: system prompt, project instruction files (`AGENTS.md`, `CLAUDE.md`, and
  similar, read as data with a size cap), selected skills, retrieved chunks, session
  history, tool results, and memory. What is dropped to fit is recorded in the trace;
  nothing is cut silently.
- Compaction: as a session nears its budget, older turns and long tool outputs are
  summarised by the `fast` role into a structured working state (goal, plan, decisions,
  files touched, open questions); recent turns stay verbatim.
- Sessions: persisted in `lokal.db` and resumable (`lokal run --resume <id>`);
  `lokal sessions ls|show|rm`.
- Memory: project and user memory as plain Markdown files the user can read and edit.
  The agent proposes memory updates; they are shown in the session and written only when
  accepted (the review idea of ADR-0005 applied to facts).
- Roles per step: planning, acting, and summarising can use different models (M2 roles).
- Skills: the router selects skills per task; a skill may narrow the tool set for its
  run, which helps small models.
- Interfaces: an interactive terminal mode that works over SSH, and a headless mode that
  streams JSON events (for Agent-Control, scripts, and CI).
- Traces: one JSONL file per run with task, selected skills, context assembly decisions,
  each model call and tool call (results truncated), approvals, outcome, timings, and the
  origin of every piece of content (user file, retrieval, tool output, MCP); secrets
  redacted before writing. `lokal traces ls|show`, and an outcome signal (accept or
  reject at the end; test results from `run_shell` when present).

Tests: deterministic end-to-end tests with a scripted fake backend; context-assembly
tests with fixed budgets (priority order, nothing dropped silently); a long scripted
session that must stay within budget and keep its recorded decisions after compaction;
resume tests; policy table tests; redaction tests that fail on a planted fake secret.

Exit criteria: on the target profile, a fixed task set (read, search, edit with approval,
run tests, a long multi-step task that forces compaction, a resumed session) completes
with a recorded success rate; no write or exec happens without approval in any test.

## M6: Coding agent and vibe coding

Goal: `lokal code` helps write and change software in a repository on the same machine,
from a small fix to a new app built from a description, with every change reviewable and
reversible (ADR-0007).

Deliverables:

- Repository understanding: file tree with ignore rules, a repository map of symbols (Go
  through `go/ast`; other languages through a pure-Go symbol extractor, since tree-sitter
  needs cgo under ADR-0001), code search, and retrieval over the repository (M3).
- Edit formats chosen per model capability: search/replace blocks, whole-file rewrites
  for small files, unified diffs for strong models. Every edit is validated (applies
  cleanly; the file still parses where a parser exists) before it is written.
- Git safety: work on a branch or in a separate worktree; one checkpoint commit per
  accepted step; `lokal code undo`; lokal never pushes.
- Verify loop: detect build, lint, and test commands (Makefile, `go.mod`, `package.json`,
  `pyproject.toml`, `Cargo.toml`, and others), run them after edits under the exec
  approval rules, feed failures back, stop at a step or time budget.
- Vibe coding: `lokal code new "<description>"` scaffolds a project from template skills,
  iterates build, run, and test, and serves the app's dev server on loopback so it can be
  previewed through the same tunnel the gateway uses.
- Coding skills: per-language and per-framework skills (for example from Agent-Skills)
  selected by the router.
- Command execution isolation on top of approvals (for example a container or namespace
  sandbox when one is available on the host): decided in an ADR at the start of M6.
- Integrations: coding tools that speak the OpenAI API or MCP (opencode, Continue, Aider,
  Cline, and others) use `lokal serve` for models and `lokal mcp` for repository
  knowledge and skills; Agent-Control drives `lokal code` through the headless event
  stream; an editor protocol (for example the Agent Client Protocol) is evaluated at the
  start of M6.
- Coding eval set: fixed repository tasks (fix a failing test, add a function with tests,
  a small refactor, scaffold a small web app) scored by tests passing, per model and
  profile, run by `make eval-code`.

Tests: edit-format application and validation against fixture files (including malformed
edits from weak models); git checkpoint and undo in temporary repositories; verify-loop
tests with a scripted fake backend and fake build commands.

Exit criteria: on the target profile the coding eval runs end to end with a recorded pass
rate per model; every edit made during the eval is in git and can be undone; no command
runs outside the approval rules.

## M7: Skill forge

Goal: lokal proposes new skills from your own repeated, successful work, proves they
help, and lets you promote them (ADR-0005).

Deliverables:

- `lokal forge mine`: cluster successful traces (agent, coding, and later multi-agent
  runs) by task embedding and tool-sequence signature; a cluster needs a minimum number of
  successful runs to qualify.
- `lokal forge draft <cluster>`: the model writes a SKILL.md candidate with provenance
  (trace IDs, content origins) into `candidates/`. Coding sessions yield project-specific
  skills (how to build and test this repository) as well as general ones.
- `lokal forge eval <candidate>`: fixtures for a direct request, a paraphrase, a near-miss
  that must not trigger, and a failure or adversarial case; run with and without the
  skill on the same fixtures; report task success, steps, tokens, and trigger precision
  and recall. Unrun cases count as failures.
- `lokal forge harden <candidate>`: bounded refinement rounds (description for routing,
  body for procedure), each re-evaluated on the same fixtures.
- Checks before promotion: structural validation, secret and personal-data scan,
  provenance flag for any external content.
- `lokal skills promote|rollback|export`: promotion shows the skill, provenance, and
  evaluation; versions by content hash; export produces a folder ready to propose to
  Agent-Skills.
- Decay report: promoted skills that stopped triggering or whose fixtures now fail.

Prior art: `ws learn` in Agent-Workspace drafts a skill from recorded run decisions; the
forge generalises this to agent traces and adds evaluation.

Exit criteria: from a seeded set of traces, at least one candidate is drafted, evaluated,
hardened, and promoted, with a measured improvement over the no-skill baseline and no
near-miss misfires; a candidate built from a trace with planted injected instructions is
flagged and cannot be promoted without the flag being shown.

## M8: Multi-agent

Goal: several agents with different roles, models, tools, and skills work on one task,
and lokal uses that only where it measurably beats a single agent on local hardware
(ADR-0007).

Deliverables:

- Agent definitions as files: Markdown with YAML frontmatter (name, description, role or
  model, tools, skills, budgets) in the project's `.agents/agents/` and in the lokal home;
  validated like skills; `lokal agents ls|show|validate`. The schema is recorded in an
  ADR at the start of M8.
- Orchestrator-workers: a lead agent plans and delegates subtasks to workers that start
  with fresh, narrow contexts (the main benefit when context windows are small) and
  return structured results, not transcripts.
- Starting roles: planner, coder, reviewer, tester, researcher (retrieval). The reviewer
  gates merging a worker's changes.
- Scheduler aware of model residency (M2): workers run in parallel only when their models
  fit in memory together; otherwise they run one after another, grouped by model to avoid
  reloading.
- Isolation: each coding worker gets its own git worktree; the lead merges after review.
- Communication only through the lead (hub and spoke) with typed messages; no free-form
  agent-to-agent conversation in v1.
- Traces as a tree of agent runs; the forge learns per-role skills from them.
- Agent-to-agent protocols with external systems (for example A2A) are evaluated, not
  committed.
- Eval: the M6 coding set and a research set, single agent versus multi-agent on the same
  models and profile; multi-agent becomes a default only for task types where it wins.

Tests: orchestrator tests with scripted fake workers (delegation, typed results, failure
of one worker, cancellation); scheduler tests against fake memory budgets; worktree
isolation and merge tests in temporary repositories.

Exit criteria: at least one task type shows a measured gain over a single agent on the
target profile; parallel workers never exceed the memory budget in any test or eval run.

## M9: v1.0

Goal: easy to install, stable to depend on.

Deliverables:

- Release workflow: binaries for five platforms, checksums, build provenance
  attestation, SBOM; signed tags.
- `install.sh` that verifies checksums; Homebrew tap.
- `THIRD_PARTY_LICENSES.md` generated from the module graph and checked in CI.
- Stable surfaces declared and documented: CLI commands and flags, `--json` output
  shapes, `config.yaml` schema version, gateway endpoints, MCP tool names, the headless
  JSON event stream, the session format, and the agent definition format.
- Linux GPU path verified (NVIDIA with vLLM or llama-server); Windows support decision
  recorded as an ADR.
- Documentation per command, troubleshooting by doctor check ID.

Exit criteria: a user installs with one command on macOS and Linux, `lokal setup` and
`lokal doctor` are green on all three profiles, and the stable surfaces have tests that
pin them.

## Not planned for v1

- Running models in-process or writing inference kernels.
- A web UI. Agent-Control is the web control panel; lokal's own interfaces are the
  terminal and the headless JSON event stream.
- Free-form multi-agent conversations or open-ended agent swarms.
- Pushing to remotes, deploying, or publishing on its own.
- Fine-tuning or LoRA training.
- Multi-user or networked server deployments.
- Native Windows before the M9 decision.

## Candidates after v1

Recorded so they are not lost; each needs its own evaluation before it is planned.

- Vision in the agent loop: screenshots of a running app checked by a vision model, using
  browser tools from an MCP server.
- Agent-to-agent interoperability with external systems (for example A2A).
- Shared team memory and skills across several lokal hosts.
- Voice input and output.
