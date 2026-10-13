# ADR-0010: Remote access, SSH tunnels, WireGuard overlays, and Gateway-as-Backend

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono, Antigravity

## Context

Many developers run local AI workloads across two distinct machines (ADR-0008, ROADMAP M2):
1. A **workstation or laptop** (macOS or Linux) where the developer writes code, edits files, and interacts with tools like Agent-Control, Claude Code, or opencode. These machines often have limited RAM or battery life and should not store massive model weights.
2. A **dedicated server or headless VM** (Linux with GPU passthrough or high RAM) running `lokal serve` and backend inference engines (Ollama, llama-server, vLLM).

ADR-0006 mandates that lokal remains strictly local-only by default, binding exclusively to loopback (`127.0.0.1`) with per-process bearer authentication to prevent DNS rebinding and unintended network exposure.

We need a clear architectural model for how:
- Agent hosts on a laptop communicate with `lokal serve` running on a remote headless VM.
- A local `lokal` process on a workstation (running `lokal mcp` or `lokal index`) can treat the remote VM's gateway as its backend (`gateway` or `remote` adapter), requiring zero local models on the workstation.
- MCP tools can be exposed remotely without opening additional HTTP ports.

## Decision

1. **Loopback-First Remote Topology via Encrypted Tunnels:**
   - On the remote VM, `lokal serve` continues to bind strictly to loopback (`127.0.0.1:52140`).
   - Network reachability from workstations is established via encrypted point-to-point tunnels:
     - **SSH Port Forwarding:** `ssh -N -L 52140:127.0.0.1:52140 user@remote-vm`
     - **WireGuard / Overlay Networks (e.g. Tailscale):** Directly routes to the node interface when explicit remote binding is chosen with token protection.
   - Because the forwarded port maps to `127.0.0.1:52140` on the client workstation:
     - The client connects to `http://127.0.0.1:52140/v1` as a local service.
     - Agent-Control treats it as a loopback provider, passing its egress policy without requiring remote network approvals.
     - Host header validation on the remote server accepts `Host: 127.0.0.1:52140` or `localhost:52140`.

2. **Remote Gateway as Backend (`RemoteAdapter`):**
   - We introduce a `remote` (or `gateway`) adapter in `internal/backend`.
   - On a workstation with no locally installed inference engines, `config.yaml` configures:
     ```yaml
     backends:
       remote:
         endpoint: "http://127.0.0.1:52140"
         token: "<vm-bearer-token>"
         enabled: true
     ```
   - The `RemoteAdapter` implements the full `backend.Adapter` interface:
     - Proxies `Chat` and `ChatStream` over `/v1/chat/completions`.
     - Proxies `Embed` over `/v1/embeddings`.
     - Queries `/v1/models` and `/healthz` for model list and running processes.
     - Relies on the remote gateway's residency manager and queueing.

3. **Remote MCP Over SSH Stdio:**
   - For agent hosts needing access to MCP tools running on the VM (e.g. knowledge retrieval or skills), `lokal mcp` is executed via SSH stdio:
     ```json
     {
       "mcpServers": {
         "lokal-remote": {
           "command": "ssh",
           "args": ["-T", "user@remote-vm", "lokal", "mcp"]
         }
       }
     }
     ```
   - This eliminates the need for an open network port for MCP, leveraging SSH authentication, key exchange, and encryption.

4. **External Engine Adapters:**
   - In addition to Ollama and Remote Gateway, lokal supports direct adapters for `llama-server` (llama.cpp HTTP server), `vLLM`, and `LM Studio` using standard OpenAI endpoints combined with engine-native health checks and tokenization.

## Consequences

### Positive
- Zero network exposure of unauthenticated inference ports.
- Workstation laptops stay cool, quiet, and memory-free while accessing heavy models on remote servers.
- Full compatibility with existing agent host loopback policies (e.g. Agent-Control local egress rules).
- Single binary works identically on workstation and dedicated VM.

### Negative
- Users must configure an SSH tunnel or VPN overlay to connect remote machines.
- The remote VM's `serve.token` must be copied to the workstation's configuration.

### Neutral
- Split indexing (indexing files locally on workstation vs. on VM) uses the remote gateway purely for embeddings.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Bind 0.0.0.0 without auth on the VM | Exposes inference and GPU resources to the LAN; vulnerable to unauthorized use |
| Custom TCP framing for remote MCP | Stdio over SSH is standard, universally supported, and secured by existing SSH keys |
| Direct reverse proxy without adapter | Bypasses local context budgeting, model mapping, and fallback logic |
