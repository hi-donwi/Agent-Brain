# ADR-0006: Local only by default: loopback, per-process token, no egress, no telemetry

- **Date:** 2026-10-13
- **Status:** Accepted
- **Deciders:** Doni Wicaksono

## Context

People run AI locally to keep their files, prompts, and traces on their machine. A
gateway listening on all interfaces, an agent that can call a cloud model when the local
one is slow, or usage telemetry would each quietly break that expectation.

lokal also exposes powerful surfaces: an OpenAI-compatible gateway, an MCP server that
can search indexed files, and an agent that can run shell commands. Other processes on
the same machine (a browser page, a compromised package) can reach a loopback port.

## Decision

- `lokal serve` binds to `127.0.0.1` only. Binding elsewhere requires an explicit flag
  and prints a warning; it is never the default.
- The gateway requires a bearer token generated per process and written to a file
  readable only by the user (mode 0600) in the lokal home. Clients read it from there.
- The MCP server uses stdio by default, which needs no port at all.
- lokal makes no network calls except to configured local backends, to model registries
  during an explicit `setup`/`pull`, and to MCP servers the user configured. A cloud
  fallback backend is possible only when the user adds one to config; it is off by
  default and every request routed to it is labelled in the output and in traces.
- No telemetry, crash reporting, or update checks.
- Traces and indexes stay in the lokal home. Secrets matching known credential patterns
  are redacted before a trace is written.

## Consequences

### Positive
- Safe to run on a laptop on an untrusted network.
- Users can reason about where their data goes: nowhere unless they configured it.

### Negative
- Clients need the token, which adds a step when wiring other tools to the gateway;
  `lokal doctor` prints the exact snippet for each supported client.
- No usage data for the project; feedback comes from issues only.

### Neutral
- Remote access (another device on the LAN) is a deliberate, documented opt-in.

## Alternatives considered

| Option | Why rejected |
|---|---|
| No auth on loopback | Any local process or a DNS-rebinding web page could drive the agent |
| Bind 0.0.0.0 for convenience | Exposes files and tools to the network by default |
| Opt-out telemetry | Contradicts the reason to run locally |

## Follow-up

- [ ] M2: token file, loopback default, Host header check against DNS rebinding
- [ ] M5: redaction before trace write, with tests that refuse a planted fake secret
