# ADR-0001: lokal is one static Go binary with CGO disabled

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono

## Context

The point of Agent-Local is to make local AI easy to set up. The tool that does the setup
cannot itself need a setup: no Python environment, no Node runtime, no C toolchain, no
system libraries beyond what the OS ships. Users range from an 8 GB laptop to a Linux GPU
workstation, on macOS, Linux, and later Windows.

The sibling tools in the same account (OPS-Toolkit, DBA-Toolkit, Agent-Secure) are
already Go command-line tools with a `cmd/` + `internal/` layout, cobra, a Makefile, and
pinned CI; reusing that shape costs nothing.

The M0 spike (pure-Go SQLite, see ADR-0003) confirmed that the storage we need builds with
`CGO_ENABLED=0` and cross-compiles statically to linux/arm64 and windows/amd64.

## Decision

We ship lokal as a single Go binary per platform, built with `CGO_ENABLED=0`, for
darwin/arm64, darwin/amd64, linux/amd64, linux/arm64, and windows/amd64. A dependency that
requires cgo is not accepted without a superseding ADR. CI builds every platform and
fails if the Linux binaries are not statically linked (`make static-check`).

## Consequences

### Positive
- Installation is "download one file" (later: `install.sh`, Homebrew tap).
- The same binary is the CLI, the gateway server, and the MCP server.
- Cross-compilation needs no per-platform toolchains in CI.

### Negative
- Native-only libraries are off limits: `sqlite-vec`, tree-sitter grammars, ONNX runtime,
  llama.cpp bindings. Each needed capability must come from pure Go or from a backend
  over HTTP (ADR-0002).
- Pure-Go SQLite is slower than the C build for heavy write loads (spike: 100k chunks with
  FTS indexing in 13-27 s). Acceptable for personal corpora.
- `go test -race` needs cgo; only the `race` Makefile target turns it back on.

### Neutral
- Python-first ecosystems (LlamaIndex, LangChain) are not used; the pieces lokal needs
  (chunking, hybrid retrieval, a tool loop) are small enough to own.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Python package (pip/uv) | Needs a managed Python install first; version conflicts are the setup pain this project exists to remove |
| TypeScript/Node (shared with Agent-Control) | Needs Node >= 22 on the target machine; single-file packaging of Node is heavy and fragile |
| Go with cgo (mattn/go-sqlite3 + sqlite-vec) | Per-platform C toolchains in CI and on contributors' machines; no static cross-compile |
| Rust | No existing code or tooling in this account to reuse; Go already covers the needs |

## Follow-up

- [x] `make static-check` and the `cross-compile` CI job enforce this
- [ ] M9: `THIRD_PARTY_LICENSES.md` generated from the module graph, as OPS-Toolkit does
