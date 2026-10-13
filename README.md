# Agent-Local

**lokal** sets up and runs AI on hardware you control, a workstation or a dedicated
headless VM that your other machines reach through a tunnel: it picks and installs a local
inference backend and models that fit your hardware, serves them behind one
OpenAI-compatible endpoint, indexes your files for retrieval with citations, runs a small
tool-using agent with approvals, and learns reusable skills from your own work, which
you review before they are used.

It is one static binary. It does not run models itself; it drives the engines that do
(Ollama, llama.cpp, LM Studio, MLX-LM, vLLM). Nothing leaves your machine unless you
configure it to.

> **Status: pre-alpha (milestone M0).** Only `lokal version` exists today. The
> [roadmap](ROADMAP.md) lists what lands in which release.

## Planned commands

| Command | What it will do | Milestone |
|---|---|---|
| `lokal setup`, `lokal doctor` | Detect hardware and backends, install and pull for a profile, verify end to end | M1 |
| `lokal serve` | One local OpenAI-compatible endpoint with stable model aliases | M2 |
| `lokal index`, `lokal ask`, `lokal search` | Local hybrid retrieval with `path:line` citations | M3 |
| `lokal skills`, `lokal mcp` | Agent Skills (SKILL.md) runtime; MCP server for other agents | M4 |
| `lokal run` | Small local agent with tool approvals and traces | M5 |
| `lokal forge` | Draft, evaluate, and harden skill candidates from your traces | M6 |

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
  SQLite, the skill format, the human promotion gate, and local-only defaults.

## Related projects

- [Agent-Skills](https://github.com/hi-donwi/Agent-Skills): portable skills in the same
  SKILL.md format lokal loads and exports.
- [Agent-Control](https://github.com/hi-donwi/Agent-Control): web control panel that can
  use `lokal serve` as a provider.
- [Agent-Secure](https://github.com/hi-donwi/Agent-Secure): the secret and dependency
  gate this repository's CI runs.

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE). lokal does not
distribute model weights; models keep their publishers' licences.
