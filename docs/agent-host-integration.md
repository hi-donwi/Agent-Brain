# Agent Host Integration Guide - lokal v1.0

`lokal` is designed to be the local intelligence layer under existing coding agents and
IDEs, not another agent (ADR-0008). It exposes two standard integration surfaces:
1. **OpenAI-compatible HTTP Gateway (`lokal serve`):** Serves local models behind `http://127.0.0.1:8080/v1`.
2. **Model Context Protocol Stdio Server (`lokal mcp`):** Provides read-only repository maps, code search, knowledge retrieval, and Agent Skills.

---

## 1. Agent-Control Integration

Agent-Control connects to `lokal` as both an inference client and an MCP host.

### Gateway Connection:
1. Start the gateway:
   ```bash
   lokal serve &
   ```
2. Read the bearer token:
   ```bash
   TOKEN=$(cat ~/.lokal/serve.token)
   ```
3. Configure Agent-Control endpoint:
   - **Base URL:** `http://127.0.0.1:8080/v1`
   - **API Key:** `$TOKEN`
   - **Model:** `code` (or `chat`)

### MCP Integration:
In Agent-Control MCP settings:
```json
{
  "mcpServers": {
    "lokal": {
      "command": "lokal",
      "args": ["mcp"]
    }
  }
}
```

### Trace Export to Skill Forge:
Export Agent-Control sessions for skill learning:
```bash
lokal traces import /path/to/agent-control/session-123.json
lokal forge mine
```

---

## 2. Claude Code Integration

Claude Code can consume local project knowledge, symbols, and Agent Skills through `lokal mcp`.

### Configuration (`~/.claude.json` or `.claude/mcp.json`):
Add `lokal` as an MCP server:
```json
{
  "mcpServers": {
    "lokal": {
      "command": "lokal",
      "args": ["mcp"]
    }
  }
}
```

Claude Code will automatically discover and use the following read-only tools:
- `repo_map`: Ranked outline of file symbols within token limits.
- `find_symbol`: Instant symbol definitions and references.
- `search_code`: Fast regular expression search across source code.
- `search_knowledge`: Semantic and lexical search over indexed collections.
- `list_skills` and `get_skill`: Progressive disclosure of Agent Skills.

---

## 3. Continue (VS Code & JetBrains)

Configure Continue to use `lokal`'s gateway for local code completions and chat.

In `~/.continue/config.json`:
```json
{
  "models": [
    {
      "title": "lokal Code",
      "provider": "openai",
      "model": "code",
      "apiBase": "http://127.0.0.1:8080/v1",
      "apiKey": "PASTE_TOKEN_FROM_SERVE_TOKEN"
    },
    {
      "title": "lokal Chat",
      "provider": "openai",
      "model": "chat",
      "apiBase": "http://127.0.0.1:8080/v1",
      "apiKey": "PASTE_TOKEN_FROM_SERVE_TOKEN"
    }
  ],
  "tabAutocompleteModel": {
    "title": "lokal Fast",
    "provider": "openai",
    "model": "fast",
    "apiBase": "http://127.0.0.1:8080/v1",
    "apiKey": "PASTE_TOKEN_FROM_SERVE_TOKEN"
  }
}
```

---

## 4. Aider

Aider pairs with `lokal` using standard OpenAI environment variables.

### Shell setup:
```bash
# Export endpoint and authentication token
export OPENAI_API_BASE=http://127.0.0.1:8080/v1
export OPENAI_API_KEY=$(cat ~/.lokal/serve.token)

# Launch Aider using the 'code' role alias
aider --model openai/code
```

---

## 5. Remote Host over SSH Tunnel

When running `lokal` on a remote Linux GPU VM and coding on a local Mac/laptop:
```bash
# 1. Forward gateway port over SSH
ssh -N -L 8080:127.0.0.1:8080 user@remote-gpu-vm &

# 2. Copy the token to local machine
scp user@remote-gpu-vm:~/.lokal/serve.token ~/.lokal/serve.token

# 3. Local tools (Claude Code, Continue, Aider) connect to http://127.0.0.1:8080/v1 seamlessly
```
