# ADR-0016: Narrow Brain-origin management console

- **Date:** 2026-11-10
- **Status:** Accepted
- **Deciders:** Project maintainer
- **Amends:** ADR-0008 (no general web UI; defines a narrow operator-console exception)

## Context

ADR-0008 keeps the agent loop and general web console in Agent-Control and Brain as a
standalone local platform. Agent-Control must not receive provider API keys, bridge
credentials, or the Brain gateway master token. A management API alone cannot meet the
operator goal if a secret must be typed into Agent-Control's UI or proxied through its
server.

## Decision

Brain may serve a narrowly scoped, Brain-origin operator console at `/manage-ui/`,
embedded by Agent-Control Settings. It may manage only Brain configuration and local
account lifecycle through typed, management-authenticated APIs. The page owns provider
credential inputs and OAuth callback state; values are not sent to Agent-Control or
persisted by the browser. The management API remains same-origin only.

This is not a general Brain web UI: no agent loop, repository operations, shell/CLI
runner, approvals, coding sessions, or generic task runner is permitted. Native client
login remains CLI-owned where the official client requires a local interactive session.
Other Brain subsystems require separate typed API, data-boundary, and threat-model review
before any management surface is added.

## Consequences

### Positive

- Agent-Control can provide a unified operator entry point without handling upstream
  provider secrets.
- Brain retains ownership of account, token, bridge, and callback lifecycle.
- ADR-0008's no-agent-loop/platform boundary remains unchanged.

### Negative

- Brain distributes a small static UI asset set in addition to its CLI/API.
- The embedded UI is same-origin with Brain, so browser framing and API origin policies
  require explicit allowlists and tests.
- The UI intentionally covers only typed configuration operations, not every Brain CLI
  command.

## Alternatives considered

| Option | Why rejected |
|---|---|
| Provider key form and management token in Agent-Control | Violates its explicit rule that provider keys stay Brain-side and adds secrets to the host UI state. |
| Proxy management API through Agent-Control | Makes the host server a path for upstream secrets and bypasses Brain origin isolation. |
| Generic CLI or shell runner | Turns the management credential into remote command execution and bypasses typed validation. |
| No operator UI; CLI only | Does not satisfy the requested Agent-Control management workflow. |

## Follow-up

- [ ] Keep the Brain-origin UI and API narrowly typed; add tests for same-origin, framing,
      management authorization, and secret redaction for each new operation.
- [ ] Add other Brain capability families only after their resource and side-effect
      boundaries are documented individually.
