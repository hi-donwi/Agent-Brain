# Agent-Local

**lokal** sets up and runs AI on hardware you control, a workstation or a dedicated
headless VM that your other machines reach through a tunnel: it picks and installs a local
inference backend and models that fit your hardware, serves them behind one
OpenAI-compatible endpoint, indexes your files and code for retrieval with citations,
serves that knowledge and your skills to agents over MCP, and learns reusable skills from
how your agents work, which you review before they are used.

lokal is the local layer under your agents, not another agent. Agent-Control, Claude
Code, opencode, Aider, Continue, Cline, and similar tools run the loop, edit code, and
coordinate sub-agents; lokal makes local models, knowledge, and skills good enough for
them, and never writes to your repositories itself.

It is one static binary. It does not run models itself; it drives the engines that do
(Ollama, llama.cpp, LM Studio, MLX-LM, vLLM). Nothing leaves your machine unless you
configure it to.

> **Status: pre-alpha (milestone M1).** Only `lokal version` is usable today. The
> [roadmap](ROADMAP.md) lists what lands in which release.

## Planned commands

| Command | What it will do | Milestone |
|---|---|---|
| `lokal setup`, `lokal doctor` | Detect hardware and backends, install and pull for a profile, verify end to end | M1 |
| `lokal serve`, `lokal service` | One local OpenAI-compatible endpoint; roles over several models within memory | M2 |
| `lokal index`, `lokal ask`, `lokal search` | Local hybrid retrieval with `path:line` citations | M3 |
| `lokal skills`, `lokal mcp` | Agent Skills (SKILL.md) runtime; MCP server for other agents | M4 |
| `lokal mcp` code tools | Repo map, symbol and code search, project commands and instructions (read-only) | M5 |
| `lokal traces`, `lokal eval` | Capture and import agent traces; measure models and skills on this hardware | M6 |
| `lokal forge` | Learn, evaluate, and harden skill candidates from your traces | M7 |

Code assistance, vibe coding, and multi-agent work come from your agent host using these
pieces: point it at `lokal serve` for models and at `lokal mcp` for knowledge and skills.
See [ADR-0008](docs/adr/0008-local-ai-platform-agent-hosts-own-the-loop.md).

## Build from source

Requires Go 1.26 or newer.

```sh
git clone https://github.com/hi-donwi/Agent-Local.git
cd Agent-Local
make build
./bin/lokal version
```

| Make target | Purpose |
|---|---|
| `make check` | Everything CI runs on each push: gofmt, tidy, vet, race tests, smoke run |
| `make test` | Unit tests |
| `make cross` | Static binaries for darwin, linux, and windows into `bin/` |
| `make vulncheck` | govulncheck at a pinned version |

## Design

- [Architecture](docs/architecture.md): components, lokal home layout, request,
  retrieval, skill, and learning paths, security model, hardware profiles.
- [Architecture decisions](docs/adr/README.md): why Go, why orchestrate backends, why
  SQLite, the skill format, the human promotion gate, local-only defaults, and why the
  agent loop stays in agent hosts.

## Related projects

- [Agent-Skills](https://github.com/hi-donwi/Agent-Skills): portable skills in the same
  SKILL.md format lokal loads and exports.
- [Agent-Control](https://github.com/hi-donwi/Agent-Control): agent host and web console
  for Agent-Workspace; uses `lokal serve` for local models and `lokal mcp` for knowledge
  and skills.
- [Agent-Secure](https://github.com/hi-donwi/Agent-Secure): the secret and dependency
  gate this repository's CI runs.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). lokal does not
distribute model weights; models keep their publishers' licences.
