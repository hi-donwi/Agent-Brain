# Agent-Brain

**Agent-Brain** (`brain`, alias `lokal`) is the central intelligence platform and AI router
for AI coding agents, developers, and tools in the Agent-Workspace ecosystem.

It provides a unified bridge connecting coding CLIs (**Claude Code**, **Codex**,
**Antigravity**, **OpenCode**, **Aider**, **Continue**) to both local models and cloud
providers, featuring **multi-account CLI pooling**, **token optimization (RTK)**,
**auto-failover on rate limits**, **local hybrid RAG**, and **Agent Skills over MCP**.

```
┌────────────────────────────────────────────────────────────────────────┐
│                        AI CODING AGENTS & CLIS                         │
│  claude code  │  codex cli  │  antigravity (agy)  │  opencode  │ aider │
└───────────────────────────────────┬────────────────────────────────────┘
                                    │ http://127.0.0.1:52140
                                    ▼
┌────────────────────────────────────────────────────────────────────────┐
│                              AGENT-BRAIN                               │
│  • AI Router & Account Pool: priority, round-robin, auto-failover      │
│  • Token Saver (RTK): real-time tool result token compression (20-40%) │
│  • Wire Protocols: OpenAI (/v1/chat/completions) & Anthropic (/v1/msg) │
│  • Local Memory & RAG: SQLite FTS5 + Dense Vectors                     │
│  • MCP Server: read-only repository maps, symbols, and Agent Skills    │
│  • Hardware Engine Control: Ollama, llama.cpp, vLLM, LM Studio         │
└───────┬────────────────────────────────────────────────────────┬───────┘
        │                                                        │
        ▼ (Cloud Providers)                                      ▼ (Local Inference)
┌──────────────────────────────────┐        ┌────────────────────────────┐
│   Anthropic (Account Pool)       │        │   Ollama / llama.cpp       │
│   OpenAI / Codex (Account Pool)  │        │   Apple Silicon / NVIDIA   │
│   Google Gemini / Vertex AI      │        │   qwen2.5-coder, deepseek   │
│   DeepSeek / OpenRouter          │        │   Zero data leaves machine │
└──────────────────────────────────┘        └────────────────────────────┘
```

---

## Key Features

1. **AI Router & Multi-Account CLI**:
   - Manage multiple accounts per provider (e.g. Work Pro, Personal, Free Tier).
   - Dynamic selection: `priority` (subscription → cheap → free → local) or `round-robin`.
   - Automatic failover: transparently shifts requests to the next active account when encountering HTTP 429 (Rate Limit) or quota exhaustion.

2. **Token Saver (RTK)**:
   - Compresses verbose tool results (`git diff`, `grep`, test logs) on-the-fly.
   - Saves 20–40% context window tokens without losing code meaning.

3. **Coding CLI Launcher**:
   - `brain cli <tool>` launches tools like `claude`, `codex`, `agy`, or `opencode` with auto-injected proxy URLs and authentication tokens.

4. **Local AI & Hardware Orchestration**:
   - Manages local inference engines (Ollama, llama.cpp, vLLM) with strict memory residency and context limits.

5. **Local RAG & Code Intelligence**:
   - Embedded SQLite FTS5 lexical + vector search over project files.
   - AST repository mapping and symbol definitions served over stdio MCP.

---

## Commands

| Command | Description |
|---|---|
| `brain accounts list` | View configured accounts, tiers, priorities, and rate-limit cooldowns |
| `brain accounts add <provider>` | Add a provider account (`--key`, `--tier`, `--priority`) |
| `brain accounts remove <id>` | Delete a configured account |
| `brain router status` | Display router health, gateway status, and account pools |
| `brain router optimize [file]` | Benchmark Token Saver compression on diffs/logs or stdin |
| `brain cli <tool> [args...]` | Launch a CLI tool (`claude`, `codex`, `agy`, etc.) routed through Brain |
| `brain serve` | Start the OpenAI & Anthropic compatible loopback HTTP gateway |
| `brain doctor` | Check hardware, backends, accounts, and diagnose issues |
| `brain mcp` | Run read-only Model Context Protocol (MCP) server over stdio |
| `brain repomap`, `brain symbols` | Generate repository outlines and symbol search |
| `brain index`, `brain search` | Local hybrid RAG indexing and retrieval |
| `brain forge` | Mine agent traces and learn reusable skills |

---

## Quick Start

### 1. Build from Source
Requires Go 1.26 or newer.

```sh
git clone https://github.com/hi-donwi/Agent-Brain.git
cd Agent-Brain
make build
./bin/brain version
```

### 2. Configure Accounts
```sh
# Add multiple provider accounts
./bin/brain accounts add anthropic --name claude-work --key env:ANTHROPIC_WORK_KEY --tier subscription
./bin/brain accounts add anthropic --name claude-personal --key env:ANTHROPIC_PERSONAL_KEY --tier cheap
./bin/brain accounts add openai --name codex-team --key env:OPENAI_API_KEY --tier subscription
```

### 3. Start Gateway & Launch CLI Tools
```sh
# Start background gateway
./bin/brain serve &

# Launch your favorite CLI tools with routing and multi-account auto-failover
./bin/brain cli claude
./bin/brain cli codex
./bin/brain cli agy
```

---

## License

Apache License 2.0. See [LICENSE](LICENSE) and [NOTICE](NOTICE).
