# Stable Surfaces and Contracts Reference - lokal v1.0

This document defines the stable, guaranteed surfaces, schemas, and API contracts
for `lokal` v1.0. Any breaking modification requires a major version increment.

---

## 1. CLI Commands and JSON Output Shapes

All CLI commands follow POSIX flag standards and support machine-readable `--json`
flags for automated orchestrators and agents.

### 1.1 `lokal version [--json]`
Outputs version, commit hash, build date, and Go compiler version.
```json
{
  "version": "1.0.0",
  "commit": "abcdef123456",
  "date": "2026-10-13T12:00:00Z",
  "go": "go1.26.5"
}
```

### 1.2 `lokal doctor [--json]`
Inspects hardware, home layout, storage budgets, backend availability, and memory constraints.
Exit codes: `0` (healthy), `1` (warnings present), `2` (critical failures).

### 1.3 `lokal setup [--profile <p>] [--dry-run] [--yes]`
Plans and executes model installation and directory bootstrapping.
- `--dry-run`: computes disk space, model sizes, and configuration without mutating the host.
- `--yes`: executes non-interactively over headless SSH sessions.

### 1.4 `lokal models [ls|pull|rm]`
Interacts directly with the local inference engine (Ollama, llama.cpp, etc.):
- `ls [--json]`: lists installed models with sizes, digests, and modified timestamps.
- `pull <name>`: downloads a model with streaming progress updates.
- `rm <name>`: deletes an installed model from backend storage.

### 1.5 `lokal serve [--port 8080] [--profile <p>] [--allow-remote] [--trace]`
Runs the OpenAI-compatible gateway loopback server:
- `--trace`: records prompt and completion traces to `~/.lokal/traces/`.
- `--allow-remote`: binds to `0.0.0.0` (requires bearer token authorization).

### 1.6 `lokal index <path> [--collection <c>] [--recursive]`
Indexes markdown documents and code files into SQLite embeddings and FTS5 search index.

### 1.7 `lokal search <query> [--collection <c>] [--limit <n>] [--json]`
Performs hybrid lexical (FTS5) and vector retrieval over indexed collections.

### 1.8 `lokal ask <query> [--collection <c>] [--role <r>]`
Retrieves relevant file chunks and generates grounded answers citing specific file paths.

### 1.9 `lokal skills [ls|show|validate|add|promote|rollback|export]`
Manages Agent Skills adhering to the open Agent Skills specification:
- `ls [--json]`: lists skills across project, user, and configured sources.
- `validate <path> [--json]`: validates `SKILL.md` frontmatter and body format.
- `promote <candidate>`: moves evaluated candidate from `candidates/` to `skills/`.
- `rollback <name>`: restores previous backup version from `.history/`.
- `export <name> [--out <dir>]`: exports skill in portable Agent-Skills layout.

### 1.10 `lokal mcp`
Starts the stdio Model Context Protocol (MCP) server. Exposes read-only tools and project resources.

### 1.11 `lokal repomap [path] [--budget <tokens>] [--json]`
Generates an AST-parsed, ranked symbol outline of the repository fitting a token budget.

### 1.12 `lokal symbols [path] [--query <name>] [--regex <pat>] [--json]`
Locates symbol definitions and references across Go, Python, TypeScript, JavaScript, and Rust.

### 1.13 `lokal traces [ls|show|import]`
Manages recorded session logs and conversation histories:
- `ls [--json]`: summarizes sessions, turn counts, models used, and token metrics.
- `show <session-id> [--json]`: displays turn details, messages, and tool invocations.
- `import <file> [--session <id>]`: imports Agent-Control JSON or Claude Code JSONL logs.

### 1.14 `lokal eval <task-spec.yaml> [--json] [--model <name>]`
Executes declarative evaluation tasks in bounded, throwaway temporary sandboxes.

### 1.15 `lokal forge [mine|draft|eval|harden]`
The Skill Forge automated pattern extraction lifecycle:
- `mine [--min-runs <n>] [--json]`: clusters traces by tool sequences and intent keywords.
- `draft <cluster-id> [--name <name>]`: synthesizes a candidate `SKILL.md` with provenance.
- `eval <candidate-name> [--json]`: benchmarks against direct, paraphrase, and near-miss fixtures.
- `harden <candidate-name> [--rounds <n>]`: refines descriptions and procedural instructions.

### 1.16 `lokal agent [instructions|check|subagents]`
Inspects the agent policy contracts of ADR-0013. Never executes a tool call.
- `instructions [--root <dir>] [--directory <rel>]`: reads the applicable project instruction files.
- `check --tool <name> [--path <p>] [--command <c>] [--policy <file>] [--role <name>] [--definitions <dir>]`: returns `allow`, `ask`, or `deny` for one request.
- `subagents [--root <dir>] [--definitions <dir>]`: lists the known sub-agent role definitions.

---

## 2. Configuration Schema (`config.yaml`)

Configuration file resides at `~/.lokal/config.yaml` with mode `0600`.
```yaml
version: 1
profile: tiny # tiny, standard, workstation

aliases:
  chat: qwen2.5-coder-1.5b
  code: qwen2.5-coder-1.5b
  embed: nomic-embed-text
  plan: qwen2.5-coder-1.5b
  fast: qwen2.5-coder-1.5b

backends:
  primary:
    engine: ollama
    endpoint: http://127.0.0.1:11434
    timeout_seconds: 120

residency:
  max_memory_bytes: 6442450944 # 6.0 GiB
  lru_eviction: true
  queue_timeout_seconds: 30
```

---

## 3. Gateway HTTP API Contracts

All gateway endpoints (except `/healthz`) require authentication via header:
`Authorization: Bearer <token>`
The bearer token is automatically generated and stored in `~/.lokal/serve.token` with mode `0600`.

### 3.1 `GET /healthz`
Health check endpoint.
- Response: `200 OK` with `{"status":"ok","backend":"ollama","version":"1.0.0"}`

### 3.2 `GET /v1/models`
Returns available models and configured aliases.
- Object format conforms to standard OpenAI `/v1/models` specification.

### 3.3 `POST /v1/chat/completions`
OpenAI-compatible chat completion.
- Supports streaming (`stream: true`) via server-sent events (`data: {...}\n\ndata: [DONE]\n\n`).
- Accepts role aliases (`chat`, `code`, `plan`, `fast`) with automatic fallback routing.
- Context budgeting: requests exceeding context limits return `400 Bad Request` with `context_length_exceeded`.

### 3.4 `POST /v1/embeddings`
Generates vector embeddings for input strings.
- Request payload: `{"model": "embed", "input": ["text chunk"]}`

### 3.5 `POST /v1/tokens`
Token estimation endpoint to assist agent hosts in budgeting context windows:
- Request: `{"model": "code", "messages": [{"role":"user","content":"hello"}]}`
- Response: `{"model": "qwen2.5-coder:1.5b", "tokens": 12, "context_limit": 32768}`

---

## 4. MCP Tools and Resources

`lokal mcp` runs as a stdio server. Under ADR-0008, all lokal MCP tools are strictly read-only.

### Tools:
| Tool Name | Parameters | Description |
|---|---|---|
| `search_knowledge` | `query` (str), `limit` (int) | Vector and lexical search over indexed collections |
| `list_skills` | `query` (str) | Lists available Agent Skills |
| `get_skill` | `name` (str) | Retrieves `SKILL.md` body and instructions |
| `list_models` | - | Lists installed backend models and aliases |
| `repo_map` | `path` (str), `budget` (int) | Ranked symbol outline fitting token budget |
| `find_symbol` | `name` (str), `path` (str) | Finds symbol definitions and references |
| `search_code` | `pattern` (str), `path` (str) | Regular expression search across source files |
| `project_commands` | `path` (str) | Detects build, test, and lint commands |
| `project_instructions` | `directory` (str) | Reads bounded project instruction files inside the configured root |
| `check_tool_permission` | `tool` (str, required), `path` (str), `command` (str), `role` (str) | Judges a tool request with the operator policy; returns allow, ask, or deny |
| `list_subagents` | - | Lists sub-agent role definitions; operator definitions take precedence |

### Resources:
- `project://instructions`: Returns project instruction documents (`AGENTS.md`, `CLAUDE.md`).
- `project://commands`: Returns detected build and test scripts as structured JSON.
- `skills://list`: Returns catalog of available skills.

---

## 5. Trace Format Schema (v1 JSONL)

Traces are stored append-only in `~/.lokal/traces/<session_id>.jsonl`:
```json
{
  "version": 1,
  "session_id": "sess-12345",
  "turn_id": "turn-1",
  "timestamp": "2026-10-13T14:00:00Z",
  "model": "qwen2.5-coder-1.5b",
  "role": "code",
  "backend": "ollama",
  "messages": [
    {"role": "user", "content": "fix test"}
  ],
  "response": "Here is the fix...",
  "tool_calls": [
    {"name": "search_code", "result": "match found"}
  ],
  "duration_ms": 250,
  "prompt_tokens": 45,
  "eval_tokens": 120
}
```
