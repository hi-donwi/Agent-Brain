# Roadmap

Agent-Local is built in eight milestones. Each one ships something usable on its own and
ends with explicit exit criteria. Milestones are sequential; work inside a milestone is
split into small commits, one concern each, with tests first for logic.

Status: **M0 in progress.** Nothing beyond `lokal version` is implemented yet.

| Milestone | Release | Theme | Status |
|---|---|---|---|
| [M0](#m0-foundation) | - | Foundation: repo, CI, ADRs, spikes | In progress |
| [M1](#m1-setup-and-doctor) | v0.1 | Setup and doctor | Planned |
| [M2](#m2-gateway) | v0.2 | Gateway and more backends | Planned |
| [M3](#m3-local-retrieval) | v0.3 | Local retrieval (RAG) | Planned |
| [M4](#m4-skills-runtime-and-mcp-server) | v0.4 | Skills runtime and MCP server | Planned |
| [M5](#m5-local-agent) | v0.5 | Local agent | Planned |
| [M6](#m6-skill-forge) | v0.6 | Skill forge | Planned |
| [M7](#m7-v10) | v1.0 | Distribution and stability | Planned |

## Principles

- **The tiny profile is the bar.** Every feature must work on an 8 GB machine with a few
  GB of free disk. Larger machines get better models, not different code paths.
- **Fail loudly instead of degrading silently.** Truncated prompts, truncated embeddings,
  missing models, and full disks are errors with a fix, never quiet behaviour.
- **Orchestrate, do not reimplement.** Engines run models; lokal sets them up, routes,
  checks, retrieves, and learns (ADR-0002).
- **Local by default.** No egress, no telemetry, loopback only (ADR-0006).
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
  search at 100k x 768 on the smallest target; 3-4 s cold load (ADR-0003).

Exit criteria: CI green on `main`; ADRs accepted; spike results recorded.

## M1: Setup and doctor

Goal: from a fresh machine to a working local chat and embedding model with one command,
in under ten minutes on a normal connection, and a doctor that explains every failure.

Deliverables:

- `internal/hw`: CPU model and cores, total and available memory, Apple Silicon unified
  memory, NVIDIA (`nvidia-smi`) and AMD (`rocm-smi`) GPUs with VRAM, free disk on the
  model directory. macOS and Linux first.
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
  privileges; pull models; write `config.yaml`; finish with the doctor.
- `lokal doctor [--json]`: hardware summary, backend reachable and version, every alias
  resolves to a present model, configured context fits in memory, free disk, and a smoke
  test (one chat round trip, one embedding) with tokens per second. Each failed check
  prints the command that fixes it. Exit codes: 0 healthy, 1 warnings, 2 failures.
- `lokal models ls|pull|rm`, thin wrappers that respect the catalog and disk checks.

Tests: hardware parsing from captured command output fixtures per OS; adapter tests
against recorded Ollama responses (the M0 spike cases); setup planner as a pure function
from (hardware, installed state, catalog) to plan.

Exit criteria: on a clean 8 GB Apple Silicon machine `lokal setup --yes` followed by
`lokal doctor` is green; the same on a Linux machine without GPU; doctor output for
"backend not running", "model missing", and "disk too full" is actionable.

## M2: Gateway

Goal: one stable local endpoint that any OpenAI-compatible tool can use, over any
supported engine, without silent truncation.

Deliverables:

- `lokal serve [--port] [--profile]`: `GET /v1/models`, `POST /v1/chat/completions`
  (streaming with usage, tools, `response_format`), `POST /v1/embeddings`, `GET /healthz`.
- Aliases (`chat`, `code`, `embed`, `rerank`) resolved per profile; backend model names
  pass through unchanged.
- Token budget guard: requests that exceed the configured context fail with an
  OpenAI-shaped `context_length_exceeded` error. The token-count strategy per backend
  (tokenize endpoint or bundled tokenizer) is decided and recorded at the start of M2.
- Security per ADR-0006: loopback bind, per-process bearer token in a 0600 file,
  Host header check against DNS rebinding.
- Adapters for llama.cpp `llama-server`, LM Studio, MLX-LM server, and vLLM, each
  passing the shared contract test suite against recorded fixtures.
- Client snippets in `lokal doctor` for Agent-Control, Claude Code-compatible tools,
  opencode, Continue, and generic OpenAI SDKs.

Tests: contract suite per adapter; gateway tests with an in-process fake backend
(streaming, tool calls, errors, cancellation); auth and Host header tests.

Exit criteria: Agent-Control uses `lokal serve` as a provider for chat and tool calls;
every listed backend passes the contract suite.

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
re-measured on real text.

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

## M5: Local agent

Goal: a small, safe tool-using agent that works with small local models, and records
traces good enough to learn from.

Deliverables:

- `lokal run "<task>" [--dir] [--model]`: bounded loop (step and token limits), thinking
  off by default, native tool calls with a JSON-schema fallback for models without tool
  support, validation errors fed back for retry.
- Tools: `read_file`, `list_dir`, `search_files`, `search_knowledge`, `write_file` (diff
  preview), `run_shell`, and tools from configured MCP servers (MCP client).
- Policy: tools classed read, write, exec, network; write and exec require approval
  unless an explicit allow rule matches; paths confined to `--dir`; no network tools by
  default.
- Skills: the router selects skills per task; a skill may narrow the tool set for its
  run, which helps small models.
- Traces: one JSONL file per run with task, selected skills, each model call and tool
  call (results truncated), approvals, outcome, timings, and the origin of every piece of
  content (user file, retrieval, tool output, MCP); secrets redacted before writing.
- `lokal traces ls|show`, and an outcome signal: `lokal run` asks for accept or reject
  at the end; test results from `run_shell` are recorded when present.

Tests: deterministic end-to-end tests with a scripted fake backend; policy table tests;
redaction tests that fail on a planted fake secret.

Exit criteria: on the tiny profile, a fixed task set (read, search, edit with approval,
run tests) completes with a recorded success rate; no write or exec happens without
approval in any test.

## M6: Skill forge

Goal: lokal proposes new skills from your own repeated, successful work, proves they
help, and lets you promote them (ADR-0005).

Deliverables:

- `lokal forge mine`: cluster successful traces by task embedding and tool-sequence
  signature; a cluster needs a minimum number of successful runs to qualify.
- `lokal forge draft <cluster>`: the model writes a SKILL.md candidate with provenance
  (trace IDs, content origins) into `candidates/`.
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

## M7: v1.0

Goal: easy to install, stable to depend on.

Deliverables:

- Release workflow: binaries for five platforms, checksums, build provenance
  attestation, SBOM; signed tags.
- `install.sh` that verifies checksums; Homebrew tap.
- `THIRD_PARTY_LICENSES.md` generated from the module graph and checked in CI.
- Stable surfaces declared and documented: CLI commands and flags, `--json` output
  shapes, `config.yaml` schema version, gateway endpoints, MCP tool names.
- Linux GPU path verified (NVIDIA with vLLM or llama-server); Windows support decision
  recorded as an ADR.
- Documentation per command, troubleshooting by doctor check ID.

Exit criteria: a user installs with one command on macOS and Linux, `lokal setup` and
`lokal doctor` are green on all three profiles, and the stable surfaces have tests that
pin them.

## Not planned for v1

- Running models in-process or writing inference kernels.
- A chat UI (Agent-Control and other clients cover this).
- Fine-tuning or LoRA training.
- Multi-user or networked server deployments.
- Native Windows before the M7 decision.
