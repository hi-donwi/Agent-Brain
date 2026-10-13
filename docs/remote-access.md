# Remote Access and Dedicated VM Guide

This guide describes how to run `lokal` on a dedicated remote server or headless VM
(e.g., a Linux machine with high RAM or GPU passthrough) while accessing its models,
gateway endpoints, and MCP tools from your local laptop or workstation.

## Overview

In this deployment topology:
1. The **Remote VM** runs the inference engine (Ollama, llama-server, vLLM) and `lokal serve`.
   The gateway binds strictly to `127.0.0.1:52140` behind per-process bearer authentication
   (ADR-0006, ADR-0009).
2. The **Workstation / Laptop** runs developer tools (Agent-Control, Claude Code, opencode,
   editors, scripts) and optionally a lightweight `lokal` instance for local MCP retrieval.
3. An **SSH Tunnel** forwards the remote loopback port to your local machine, creating a secure,
   encrypted bridge without exposing any ports to the public internet or local network.

```
+-----------------------------------+             +------------------------------------+
| Local Workstation (Mac / Linux)   |             | Dedicated Remote VM (Linux / GPU)  |
|                                   |             |                                    |
| +-------------------------------+ |  SSH Tunnel | +--------------------------------+ |
| | Agent-Control / Claude Code / | |   (Port     | | lokal serve                    | |
| | Editors / lokal CLI           | |  52140)     | | (Bound to 127.0.0.1:52140)     | |
| |                               | | ----------> | |                                | |
| | http://127.0.0.1:52140/v1     | |             | | Bearer Token in serve.token    | |
| +-------------------------------+ |             | +--------------------------------+ |
|                                   |             |                 |                  |
|                                   |             | +--------------------------------+ |
|                                   |             | | Ollama / llama-server / vLLM   | |
|                                   |             | +--------------------------------+ |
+-----------------------------------+             +------------------------------------+
```

---

## 1. Setting Up the Remote VM

### Step 1.1: Install and Run lokal as a Service

On your remote VM:
```bash
# 1. Run automated setup (unattended mode over SSH)
lokal setup --yes

# 2. Verify all checks pass
lokal doctor

# 3. Install lokal serve as a persistent user systemd service
lokal service install
```

The service will start automatically and persist across reboots.

### Step 1.2: Retrieve the Bearer Token

View the generated bearer token on the remote VM:
```bash
cat ~/.lokal/serve.token
```
Keep this token handy; you will configure it on your local workstation.

---

## 2. Connecting from Your Workstation

### Step 2.1: Open an SSH Port Forwarding Tunnel

Run the following command on your workstation to securely forward the remote gateway port to
your local machine:

```bash
ssh -N -L 52140:127.0.0.1:52140 user@remote-vm-ip
```

Options explained:
- `-N`: Do not execute a remote command (port forwarding only).
- `-L 52140:127.0.0.1:52140`: Bind local port `52140` to remote loopback address `127.0.0.1:52140`.

#### Making the Tunnel Persistent (Optional)

To keep the tunnel running in the background automatically, you can use `autossh`:
```bash
autossh -M 0 -f -N -L 52140:127.0.0.1:52140 user@remote-vm-ip
```

Or add a Host entry in `~/.ssh/config`:
```sshconfig
Host lokal-vm
    HostName remote-vm-ip
    User user
    LocalForward 52140 127.0.0.1:52140
    ServerAliveInterval 60
    ServerAliveCountMax 3
```
Then simply run:
```bash
ssh -N lokal-vm
```

---

## 3. Workstation Split Deployment (Gateway-as-Backend)

If you wish to run `lokal` commands locally (such as `lokal doctor`, `lokal models`, or future
`lokal mcp` indexing local files) without having any inference models or Ollama installed on
your laptop, configure the remote gateway as your backend.

Edit `~/.lokal/config.yaml` on your workstation:
```yaml
version: 1
profile: tiny
backends:
  remote:
    endpoint: "http://127.0.0.1:52140"
    token: "<paste-remote-bearer-token-here>"
    enabled: true
aliases:
  chat: "qwen2.5-coder-1.5b"
  code: "qwen2.5-coder-1.5b"
  embed: "nomic-embed-text"
```

Verify the connection:
```bash
lokal doctor
```

Output will confirm that the `remote` backend is reachable at `http://127.0.0.1:52140` and
models are verified.

---

## 4. Configuring Agent Hosts and Client Tools

Because the SSH tunnel forwards the remote port to `127.0.0.1:52140`, all client tools interact
with `lokal` as if it were running locally on your workstation.

### Agent-Control
In Agent-Control settings:
- **Base URL:** `http://127.0.0.1:52140/v1`
- **API Key:** `<remote-bearer-token>`
- **Model:** `chat` (or `code`)

Because `127.0.0.1` is loopback, Agent-Control treats it as a local provider and does not require
external egress permissions.

### Claude Code Compatible Tools / OpenAI SDKs
Export the following environment variables:
```bash
export OPENAI_BASE_URL="http://127.0.0.1:52140/v1"
export OPENAI_API_KEY="<remote-bearer-token>"
```

### Python SDK
```python
from openai import OpenAI

client = OpenAI(
    base_url="http://127.0.0.1:52140/v1",
    api_key="<remote-bearer-token>",
)

response = client.chat.completions.create(
    model="chat",
    messages=[{"role": "user", "content": "Hello via remote lokal!"}],
)
print(response.choices[0].message.content)
```

---

## 5. Remote MCP Over SSH Stdio

If you want an agent host (such as Claude Code or Cursor) on your workstation to execute MCP tools
hosted on the remote VM, run `lokal mcp` directly over SSH stdio:

```json
{
  "mcpServers": {
    "lokal-remote": {
      "command": "ssh",
      "args": ["-T", "user@remote-vm-ip", "lokal", "mcp"]
    }
  }
}
```

Benefits:
- **Zero open ports:** MCP runs over standard input/output.
- **Mutual authentication:** Secured by your existing SSH public key infrastructure.
- **Zero model exposure:** Model weights, retrieval stores, and GPU resources remain securely on the VM.
